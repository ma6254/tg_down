#!/usr/bin/env python3
"""构建 tg_down：注入版本信息，可选打包前端产物。

用法：
    python build.py                     # 构建当前平台的二进制
    python build.py --web               # 先构建前端并打包 webembed/dist.tar.gz
    python build.py --release           # 去掉符号表（-s -w），体积更小
    python build.py --version 0.2.0     # 手动指定版本号
    python build.py --os linux --arch arm64 --output bin/tg_down-linux-arm64

版本信息通过 -ldflags 写入 internal/build 包，`tg_down version` 与
GET /api/v1/system 会返回同样的值：

    Version   语义化版本（有 git tag 用 tag，没有则 dev；工作区脏时加 -dirty）
    Commit    当前 git 提交的短哈希（没有 git 时为 none）
    BuildTime 构建时间，RFC3339 带时区

--web 会调用 pnpm 构建 ../tg_down_web，并把 dist 打成 webembed/dist.tar.gz。
打包是确定性的（条目排序、mtime 归零），同一份前端产物每次生成的文件完全一致。
"""

from __future__ import annotations

import argparse
import gzip
import io
import os
import platform
import shutil
import subprocess
import sys
import tarfile
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parent
VERSION_PKG = "github.com/ma6254/tg_down/internal/build"
WEB_DIR_DEFAULT = ROOT.parent / "tg_down_web"
WEBEMBED_DIR = ROOT / "webembed"
TARBALL_NAME = "dist.tar.gz"


def _enable_utf8() -> None:
    """Windows 控制台默认不是 UTF-8，统一改成 UTF-8，避免中文输出乱码。"""
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="replace")


_enable_utf8()


def log(message: str) -> None:
    print(message, flush=True)


def die(message: str, code: int = 1) -> None:
    print(f"错误: {message}", file=sys.stderr, flush=True)
    raise SystemExit(code)


def run(cmd: list[str], cwd: Path | None = None, env: dict[str, str] | None = None) -> None:
    """执行命令；失败时抛出 SystemExit，由调用方决定退出码。"""
    log("+ " + " ".join(cmd))
    try:
        subprocess.run(cmd, cwd=str(cwd) if cwd else None, env=env, check=True)
    except FileNotFoundError:
        die(f"找不到命令 {cmd[0]}，请先安装并加入 PATH")
    except subprocess.CalledProcessError as exc:
        die(f"命令失败（退出码 {exc.returncode}）：" + " ".join(cmd))


def git(*args: str) -> str:
    """读取 git 信息；没有 git 或不在仓库里时返回空串。"""
    try:
        proc = subprocess.run(
            ["git", *args], cwd=ROOT, text=True, capture_output=True, check=False
        )
    except OSError:
        return ""
    if proc.returncode != 0:
        return ""
    return proc.stdout.strip()


def git_version() -> str:
    """版本号：优先最近的 tag，工作区脏时追加 -dirty。"""
    if not git("rev-parse", "--is-inside-work-tree"):
        return "dev"
    tag = git("describe", "--tags", "--abbrev=0")
    if not tag:
        return "dev"
    version = git("describe", "--tags", "--dirty")
    return version or tag


def git_commit() -> str:
    commit = git("rev-parse", "--short", "HEAD")
    return commit or "none"


def build_time() -> str:
    """构建时间：本地时区的 RFC3339，精确到秒。"""
    return datetime.now().astimezone().replace(microsecond=0).isoformat()


def default_output(target_os: str, target_arch: str) -> Path:
    name = "tg_down.exe" if target_os == "windows" else "tg_down"
    # 交叉编译时在文件名里带上平台，避免覆盖本机构建。
    if target_os != host_os() or target_arch != host_arch():
        name = f"tg_down-{target_os}-{target_arch}" + (".exe" if target_os == "windows" else "")
    return ROOT / "bin" / name


def host_os() -> str:
    return {"win32": "windows", "cygwin": "windows", "darwin": "darwin"}.get(
        sys.platform, sys.platform
    )


def host_arch() -> str:
    mapping = {
        "amd64": "amd64",
        "x86_64": "amd64",
        "i386": "386",
        "i686": "386",
        "arm64": "arm64",
        "aarch64": "arm64",
        "armv7l": "arm",
    }
    return mapping.get(platform.machine().lower(), "amd64")


def find_pnpm() -> str:
    for name in ("pnpm", "pnpm.cmd", "pnpm.exe"):
        found = shutil.which(name)
        if found:
            return found
    die("找不到 pnpm，请先安装（npm install -g pnpm）")
    return ""  # 到不了这里，仅让类型检查闭嘴


def build_web(web_dir: Path) -> Path:
    """构建前端并返回 dist 目录。"""
    if not (web_dir / "package.json").exists():
        die(f"前端目录不存在或不是 pnpm 项目: {web_dir}")
    pnpm = find_pnpm()
    if not (web_dir / "node_modules").exists():
        log("首次构建，先安装依赖…")
        run([pnpm, "install", "--frozen-lockfile"], cwd=web_dir)
    run([pnpm, "run", "build"], cwd=web_dir)

    dist = web_dir / "dist"
    if not (dist / "index.html").exists():
        die(f"前端构建产物缺少 index.html: {dist}")
    return dist


def pack_tarball(dist_dir: Path, target: Path) -> None:
    """把 dist 打成确定性 tar.gz，条目形如 dist/index.html。

    条目排序、mtime/uid/gid 归零，gzip 头也不带时间：同一份产物每次生成的文件一致，
    提交到仓库时不会因为构建时间产生无意义的 diff。
    """
    files = sorted(path for path in dist_dir.rglob("*") if path.is_file())
    target = target.resolve()
    target.parent.mkdir(parents=True, exist_ok=True)
    tmp = target.with_name(target.name + ".tmp")

    with open(tmp, "wb") as raw:
        # filename="" 让 gzip 头不写入输出文件名，产物与保存路径无关。
        with gzip.GzipFile(fileobj=raw, mode="wb", compresslevel=9, mtime=0, filename="") as gz:
            with tarfile.open(fileobj=gz, mode="w") as tar:
                for path in files:
                    rel = path.relative_to(dist_dir.parent).as_posix()
                    data = path.read_bytes()
                    info = tarfile.TarInfo(rel)
                    info.size = len(data)
                    info.mtime = 0
                    info.mode = 0o644
                    info.uid = info.gid = 0
                    info.uname = info.gname = ""
                    tar.addfile(info, io.BytesIO(data))

    os.replace(tmp, target)
    try:
        shown = target.relative_to(ROOT)
    except ValueError:
        shown = target
    log(f"已打包 {len(files)} 个文件 → {shown}（{target.stat().st_size} 字节）")


def build_go(args: argparse.Namespace, version: str, commit: str, when: str) -> Path:
    output = Path(args.output) if args.output else default_output(args.os, args.arch)
    if not output.is_absolute():
        output = ROOT / output
    output.parent.mkdir(parents=True, exist_ok=True)

    ldflags = [
        "-X", f"{VERSION_PKG}.Version={version}",
        "-X", f"{VERSION_PKG}.Commit={commit}",
        "-X", f"{VERSION_PKG}.BuildTime={when}",
    ]
    if args.release:
        ldflags = ["-s", "-w", *ldflags]

    cmd = ["go", "build", "-trimpath", "-ldflags", " ".join(ldflags), "-o", str(output), "."]
    env = dict(os.environ)
    env["GOOS"] = args.os
    env["GOARCH"] = args.arch
    if args.cgo is not None:
        env["CGO_ENABLED"] = "1" if args.cgo else "0"
    run(cmd, cwd=ROOT, env=env)
    return output


def describe(output: Path, version: str, commit: str, when: str, target: str, cross: bool) -> None:
    size = output.stat().st_size
    log("")
    log("构建完成")
    log(f"  产物    {output}")
    log(f"  目标    {target}")
    log(f"  版本    {version}")
    log(f"  提交    {commit}")
    log(f"  时间    {when}")
    log(f"  大小    {size // 1024} KiB")

    # 本机构建时顺手跑一次 version 子命令，确认注入生效（交叉产物无法执行）。
    if not cross:
        try:
            proc = subprocess.run(
                [str(output), "version"], text=True, capture_output=True, check=False
            )
        except OSError as exc:
            log(f"（跳过自检：{exc}）")
            return
        if proc.returncode == 0 and proc.stdout.strip():
            log("")
            log("产物自检：")
            for line in proc.stdout.strip().splitlines():
                log(f"  {line}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="构建 tg_down：注入版本信息，可选打包前端产物。",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "示例：\n"
            "  python build.py\n"
            "  python build.py --web --release\n"
            "  python build.py --os linux --arch arm64\n"
        ),
    )
    parser.add_argument("--version", default="", help="覆盖版本号（默认取 git tag，没有则 dev）")
    parser.add_argument("--os", default=host_os(), help=f"目标系统（默认 {host_os()}）")
    parser.add_argument("--arch", default="", help="目标架构（默认本机，例如 amd64 / arm64）")
    parser.add_argument("--output", default="", help="产物路径（默认 bin/tg_down[.exe]）")
    parser.add_argument("--release", action="store_true", help="去掉符号表（-s -w）")
    parser.add_argument("--web", action="store_true", help="构建前端并打包 webembed/dist.tar.gz")
    parser.add_argument(
        "--web-dir",
        default=str(WEB_DIR_DEFAULT),
        help="前端仓库目录（默认 ../tg_down_web）",
    )
    parser.add_argument(
        "--cgo",
        choices=("on", "off"),
        default=None,
        help="显式设置 CGO_ENABLED（默认沿用环境）",
    )
    args = parser.parse_args(argv)

    # 架构默认取本机；os.uname 在 Windows 上不存在，这里给出常见兜底。
    if not args.arch:
        args.arch = host_arch() or "amd64"
    if args.cgo is not None:
        args.cgo = args.cgo == "on"

    version = args.version or git_version()
    commit = git_commit()
    when = build_time()

    log(f"版本 {version} · 提交 {commit} · {when}")
    if args.web:
        dist = build_web(Path(args.web_dir).resolve())
        pack_tarball(dist, WEBEMBED_DIR / TARBALL_NAME)

    output = build_go(args, version, commit, when)
    cross = args.os != host_os() or args.arch != (host_arch() or "amd64")
    describe(output, version, commit, when, f"{args.os}/{args.arch}", cross)
    return 0


if __name__ == "__main__":
    sys.exit(main())
