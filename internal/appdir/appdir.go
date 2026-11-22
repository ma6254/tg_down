// Package appdir 负责解析 tg_down 的运行数据目录。
//
// 默认放在可执行文件所在目录（便携：拷走 exe 就带走数据），
// 可用环境变量 TG_DOWN_HOME 覆盖，便于把数据放到自定义位置（如 U 盘、同步目录）。
package appdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirName 是回退到用户主目录时使用的数据目录名（go run / go test 等场景）。
const DirName = ".tg_down"

// EnvHome 是指定数据目录的环境变量名。
const EnvHome = "TG_DOWN_HOME"

// DataDir 返回数据目录路径，不保证目录已存在。
//
// 优先级：TG_DOWN_HOME → 可执行文件所在目录 → 用户主目录下的 .tg_down。
// 最后一种只用于 go run / go test 这类临时二进制：它们每次运行路径都变，
// 数据放在旁边会丢，所以回退到固定位置。
func DataDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(EnvHome)); custom != "" {
		return custom, nil
	}
	if exe, err := os.Executable(); err == nil {
		if dir := exeDir(exe); dir != "" {
			return dir, nil
		}
	}
	return homeDataDir()
}

// homeDataDir 返回旧的默认位置 ~/.tg_down。
func homeDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("无法确定用户主目录，请设置 " + EnvHome)
	}
	return filepath.Join(home, DirName), nil
}

// exeDir 返回可执行文件所在目录；临时构建产物返回空串，由调用方回退。
//
// go run / go test 的二进制在 GOCACHE 或临时目录里，且每次路径都不同，
// 把这些路径当作数据目录会让数据凭空消失，所以统一回退到 ~/.tg_down。
func exeDir(exe string) string {
	base := strings.ToLower(filepath.Base(exe))
	if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe") {
		return ""
	}

	dir := filepath.Dir(exe)
	if within(dir, os.TempDir()) {
		return ""
	}
	if cache := strings.TrimSpace(os.Getenv("GOCACHE")); cache != "" && within(dir, cache) {
		return ""
	}
	if strings.Contains(strings.ToLower(dir), "go-build") {
		return ""
	}
	return dir
}

// within 判断 path 是否等于 root 或位于 root 之下。
func within(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// EnsureDataDir 创建数据目录（权限 0700）并返回路径。
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
	return dir, nil
}

// SessionsDir 返回 session 文件的默认目录 <数据目录>/sessions，并确保它存在。
//
// session 文件等同于账号凭证，跟数据库、头像一起放在数据目录里，
// 不再依赖启动时的工作目录；返回绝对路径，写进数据库后换目录启动也能找到。
func SessionsDir() (string, error) {
	dir, err := EnsureDataDir()
	if err != nil {
		return "", err
	}
	sessions := filepath.Join(dir, "sessions")
	abs, err := filepath.Abs(sessions)
	if err != nil {
		return "", fmt.Errorf("解析 session 目录 %s: %w", sessions, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("创建 session 目录 %s: %w", abs, err)
	}
	return abs, nil
}

// DBPath 返回 SQLite 数据文件路径，并确保所在目录存在。
func DBPath() (string, error) {
	dir, err := EnsureDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data.db"), nil
}

// RelSessionPath 把 session 文件的绝对路径转成相对数据目录的路径（存库用）。
//
// 统一用 / 分隔，数据目录整体搬走（换盘、U 盘、同步目录）后路径仍然有效；
// 文件不在数据目录内时保留绝对路径，避免出现一串 ../。
func RelSessionPath(path string) string {
	value := strings.TrimSpace(path)
	if value == "" {
		return ""
	}

	clean := filepath.Clean(value)
	if !filepath.IsAbs(clean) {
		// 已经是相对路径：视为相对数据目录，只做分隔符归一。
		return filepath.ToSlash(clean)
	}

	dir, err := DataDir()
	if err != nil {
		return filepath.ToSlash(clean)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return filepath.ToSlash(clean)
	}
	absPath, err := filepath.Abs(clean)
	if err != nil {
		return filepath.ToSlash(clean)
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(absPath)
	}
	return filepath.ToSlash(rel)
}

// AbsSessionPath 把数据库里的 session_path 还原成实际文件路径。
//
// 相对路径按数据目录解析；绝对路径（历史数据或数据目录外的文件）原样返回。
// 兼容旧版本：旧数据目录布局下文件名相对工作目录（./sessions），当数据目录里
// 找不到该文件时会退回到工作目录下查找。
func AbsSessionPath(stored string) string {
	value := strings.TrimSpace(stored)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}

	native := filepath.FromSlash(value)
	dir, err := DataDir()
	if err != nil {
		return native
	}
	candidate := filepath.Join(dir, native)
	if _, statErr := os.Stat(candidate); statErr == nil {
		return candidate
	}
	// 旧版本把 session 放在启动目录下的 sessions/，这里兜底兼容。
	if _, statErr := os.Stat(native); statErr == nil {
		return native
	}
	return candidate
}
