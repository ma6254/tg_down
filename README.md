# tg_down_server

Telegram 下载工具的 Go 后端。

把 Telegram 里能看到的媒体按任务队列下载到本地磁盘：单用户、本地优先，默认只监听 `127.0.0.1`；需要对外暴露时再启用 Bearer token。前端是一个 React 单页应用，构建后通过 `go:embed` 打进同一个可执行文件。

> **状态：HTTP 服务已接入账号与配置接口**。已实现账号探测/列表/切换/详情与配置读写；会话、消息、下载与前端内嵌尚未接入。完整规划见 [规划文档](./doc/plan.md)，接口见 [API 草案](./doc/api.md)。

## 规划中的能力

- **双接入**：MTProto 用户账号（手机号 + 验证码 + 两步密码）与 Bot API（Bot Token），统一抽象成"账号"。
- **浏览**：会话列表与消息列表，可只筛含媒体的消息。
- **下载**：任务队列、并发限流、SSE 实时进度、暂停 / 继续 / 重试、`.part` 断点续传、`FLOOD_WAIT` 自动挂起恢复。
- **单二进制**：前端产物内嵌，部署只拷贝一个可执行文件。
- **安全**：默认回环监听；非回环地址未配置 token 时拒绝启动；session 与 bot token 不写日志、不通过接口回传。

## 技术栈

| 层 | 选型 |
|---|---|
| 语言 | Go 1.27 |
| HTTP | 标准库 `net/http`（Go 1.22+ 路由） |
| 命令 | cobra |
| 存储 | SQLite，纯 Go 驱动（免 CGO） |
| Telegram | gotd/td（MTProto）+ 官方 Bot API |
| 前端 | React 19 + TypeScript + Tailwind CSS（同仓 `../tg_down_web`） |

## 目录结构（规划）

```
cmd/tgdown/        命令入口：serve / version
internal/config/   配置加载与校验
internal/account/  账号与登录流程
internal/tgclient/ 统一 Client 接口，mtproto 与 botapi 两个实现
internal/download/ 任务管理、worker、续传、限流
internal/store/    SQLite
internal/httpapi/  路由、中间件、SSE
webembed/          go:embed 前端产物
```

## 快速开始

```bash
go run . --help                   # 查看全部命令与参数
go run . version                  # 版本与构建信息
go run . serve                    # 启动 HTTP 服务（127.0.0.1:22429）

python build.py                   # 构建 bin/tg_down[.exe]，注入版本 / 提交 / 编译时间
python build.py --web --release   # 同时构建前端并打包 webembed/dist.tar.gz、去掉符号表
go build -o tg_down.exe .         # 快速构建（版本信息为 dev / none / unknown）
```

`build.py` 需要 Python 3.9+；`--web` 还会调用 `pnpm` 构建 `../tg_down_web`。
构建信息会写进 `tg_down version` 与 `GET /api/v1/system`，交叉编译用
`--os/--arch`（例如 `--os linux --arch arm64`）。

启动后可访问 `http://127.0.0.1:22429/api/v1/health` 与 `/swagger/`；
前端的 Web 界面尚未内嵌，开发期用 `../tg_down_web` 的 Vite 代理访问。

## 命令行参数

全局参数（所有子命令共用，持久化 flag）：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--workdir` | 空 | 启动前切换工作目录，相对路径都基于它解析 |
| `--download-dir` | `./downloads` | 下载根目录 |
| `--max-concurrency` | `1` | 并发下载数，范围 1–8 |
| `--auth-token` | 空 | API 鉴权 token；监听非回环地址时**必填** |
| `--botapi-base-url` | 空 | 自建 Bot API 服务地址，空则用官方 |
| `--log-level` | `info` | `debug` / `info` / `warn` / `error` |

`serve` 专用：

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--listen` | `127.0.0.1:22429` | HTTP 监听地址 |

根命令不带子命令时等价于 `serve`，因此同样接受 `--listen`；该参数不出现在
全局参数列表里，也不会出现在 `account`、`download` 等子命令的帮助中。

安全约束：监听 `0.0.0.0`、`::`、空主机或非回环 IP 时必须提供 `--auth-token`，
否则启动失败；`--max-concurrency` 限制在 1–8，避免触发 Telegram 限流。

## 账号管理

默认零配置：内置 `desktop` 应用凭据（Telegram Desktop 公开的 api_id `2040`），开箱即可登录。
需要独立配额时用 `--app custom` 配合自己申请的 `api_id` / `api_hash`（到
<https://my.telegram.org> 的 "API development tools" 创建，也可用
`TG_DOWN_API_ID` / `TG_DOWN_API_HASH` 环境变量提供）。

凭据也可以写进数据库配置，登录、在线资料与下载等所有命令都会自动使用：

```bash
tg_down config set api_id 123456
tg_down config set api_hash abcdef0123456789abcdef0123456789
tg_down config set api_custom_enabled true   # 启用自定义凭据（默认 false）
tg_down config unset api_id                  # 清除后回退内置 desktop
tg_down config unset api_hash
tg_down config unset api_custom_enabled
```

`api_custom_enabled` 关闭时，即使配置了 `api_id` / `api_hash` 也使用内置 desktop；
打开后两项必须同时配置。`api_id` 会校验为正整数，`api_hash` 校验为 32 位十六进制。
命令行 `--api-id/--api-hash` 的优先级高于数据库配置。

> 注意：`api_id` / `api_hash` 主要用于登录（`auth.sendCode`）和声明应用身份。
> 已保存 session 的授权由 session 文件里的 auth key 承载，日常请求不会重新校验
> `api_hash`；因此换一组凭据仍可能连上已有 session，但这**不代表凭据有效**。
> 请把 session 文件与账号密码同等保管。

```bash
# 零配置登录（内置 desktop 应用）
tg_down account login --phone +8613800000000

# 需要代理时先写入配置（Clash 等），后续命令自动使用
tg_down config set http_proxy http://127.0.0.1:7897
# 临时关闭代理（保留地址）
tg_down config set http_proxy_enabled false
tg_down account login --phone +8613800000000

# 使用自备应用凭据（推荐长期使用，配额独立）
tg_down account login --app custom --api-id 123456 --api-hash abcdef0123... \
  --phone +8613800000000

# 非交互：直接提供验证码与两步密码
tg_down account login --phone +8613800000000 --code 12345 --password s3cret

# 探测本机 Telegram Desktop（tdata）中已登录的账号：不登录、不发码
tg_down account login --probe

# 探测本工具自己保存的 session（多个时用 --phone 指定）
tg_down account login --probe-session
```

| 参数 | 说明 |
|---|---|
| `--kind` | `mtproto`（默认）或 `botapi` |
| `--app` | 内置应用：`desktop`（默认，零配置）或 `custom`（需自备 api 凭据） |
| `--api-id` | 自备 api_id，也可用 `TG_DOWN_API_ID`；提供即视为 `custom` |
| `--api-hash` | 自备 api_hash，也可用 `TG_DOWN_API_HASH` |
| `--name` | 展示名，留空按类型自动生成（手机号打码 / `bot:<id>`） |
| `--phone` | MTProto 手机号，E.164 格式 |
| `--token` | Bot API token，形如 `123456789:AA...` |
| `--code` | 登录验证码，留空则登录时交互输入 |
| `--password` | 两步验证密码，留空则在需要时交互输入 |
| `--probe` | 探测本机 Telegram Desktop（tdata）中已登录的账号并导入数据库，不触发登录 |
| `--probe-timeout` | 探测时连接 Telegram 的超时，默认 `30s` |
| `--probe-session` | 探测本工具自己保存的 session，不登录、不发码 |
| `--tdata` | Telegram Desktop 的 tdata 目录，留空自动查找 |
| `--tdata-passcode` | Telegram Desktop 的本地密码（设置了才需要） |

登录成功后 session 写入 `<数据目录>/sessions/mtproto-<手机号数字>.json`（默认 `~/.tg_down/sessions/`），
权限收紧到 `0600`。该文件等同于账号凭证，**不要提交或分享**（`.gitignore` 已忽略 `sessions/`）。
session 位置固定跟随数据目录，不随启动目录变化；需要整体换位置时用 `TG_DOWN_HOME`。
重复登录会复用有效 session，直接打印当前账号。

账号登记到数据库后，如果库里**只有一个账号**，会自动设为当前账号（`settings.current_account_id`），
无需再执行 `account switch`；有多个账号时保持原有选择。

代理支持 `socks5` / `socks5h`（远端 DNS）与 `http` / `https`（CONNECT 隧道），后续下载同样走它。
也可以用 `tg_down config set http_proxy ...` 持久化代理，未配置时直连；
`http_proxy_enabled=false`（默认 true）会保留代理地址但强制直连。

校验规则：`mtproto` 必须有合法手机号且不接受 `--token`；`--api-id` 与 `--api-hash` 必须成对提供；
`--app custom` 必须带 api 凭据；`botapi` 必须有形态合法的 token 且不接受 `--phone`/`--code`/`--password`。
打印时手机号、token 与代理凭据均脱敏。

> **关于内置 desktop 应用**：它使用 Telegram Desktop 的公开凭据，零配置但**共享配额**，
> 大量用户同时使用更容易触发 `FLOOD_WAIT`。长期使用建议自己申请应用后加 `--app custom`。

### 探测本机 Telegram Desktop

`--probe` 会读取 Windows 上已安装的 Telegram Desktop 的 `tdata`，用它原有的授权连一次
Telegram，输出该账号信息与已登录设备列表，并把探测成功的账号**导入数据库**——不登录、不发码、不修改 `tdata`。

```bash
tg_down account login --probe
tg_down account login --probe --tdata "C:\Users\me\AppData\Roaming\Telegram Desktop\tdata"
tg_down account login --probe --tdata-passcode 本地密码
tg_down account login --probe --probe-timeout 30s
```

自动查找顺序：`%APPDATA%\Telegram Desktop\tdata` → `%APPDATA%\TelegramDesktop\tdata` →
`%LOCALAPPDATA%\Telegram Desktop\tdata`，都没有就用 `--tdata` 指定。

```
  tdata     C:\Users\me\AppData\Roaming\Telegram Desktop\tdata
  账号数    1

  ── 账号 #0 ──
  账号      ...
  账号 ID   355927778
  用户名    @ma6254
  手机号    +861****364

  已登录客户端（2）
  * Telegram Desktop · PC · Hong Kong · 最近活跃 2026-11-18 22:47
    Telegram Android · Android · Xiaomi 14 Pro · Tai Wai, Hong Kong · 最近活跃 2026-11-18 22:31
```

说明：

- 探测成功后：授权写入 `<数据目录>/sessions/mtproto-<手机号>.json`（文件已存在时保留原文件），
  账号记录 upsert 到 `accounts` 表，随后可直接 `account list` / `account switch` 使用；
  如果导入后库里只有一个账号，会自动设为当前账号。
- 网络超时默认 **30 秒**，用 `--probe-timeout` 调整；超时会提示检查 `http_proxy` 配置。
- 代理与登录一致，只从配置 `http_proxy` / `http_proxy_enabled` 读取；输出会提示当前是否走代理。
- tdata 设置了本地密码时，需要 `--tdata-passcode`；口令错误会提示解析失败。
- 一个 tdata 里可能有多个账号，会逐个探测并分别导入。
- 只读操作：不写回 tdata；连接以 Desktop 同一应用身份（api_id 2040）进行。

### 探测本工具自己的 session

`--probe-session` 是另一个功能：检查数据目录下 `sessions/`（默认 `~/.tg_down/sessions/`）里
由本工具保存的 session 是否仍有效。

```bash
tg_down account login --probe-session
tg_down account login --probe-session --phone +8613800000000
```

输出 session 路径、登录状态、账号信息与已登录客户端；session 已失效时退出码仍为 0，
只在状态里标「未授权」——那是探测结果，不是失败。session 定位顺序：
有 `--phone` 按手机号推导，否则用 `<数据目录>/sessions` 下唯一的 `mtproto-*.json`。

### 查看与退出

```bash
tg_down account list               # 账号总览（别名 ls）
tg_down account info 3             # 查看账号详情（别名 show）
tg_down account info               # 查看当前账号详情
tg_down account info --live        # 额外联网查询资料与已登录设备（不落库）
tg_down account switch 3           # 按账号 ID 切换当前账号（别名 use）
tg_down account switch             # 显示当前账号
tg_down account logout 3           # 按 ID 退出登录
tg_down account logout my_bot      # 按展示名 / 手机号（别名 remove / delete）
tg_down account logout --all --yes # 删除全部账号
```

`info` 默认只读本地数据库，展示 ID、类型、名称、用户名、手机号（脱敏）、状态、
会员、session 路径与创建/更新时间；不带参数取当前账号，带参数取指定 ID。

加 `--live` 会临时连一次 Telegram，查询服务端最新资料（账号名、用户名、手机号、
生日、简介）与已登录客户端列表——**只展示，不写入数据库**，超时 30 秒。

「会员」一行固定输出：离线时为「未知（加 --live 查询）」，`--live` 成功后会变成「是 / 否」，
来源于服务端 `user.premium`（只知道当前是否有效，拿不到到期时间）。

```
$ tg_down account info --live

正在联网查询资料（不落库）…

账号信息（当前账号）
  ID        1
  类型      mtproto
  名称      9a6c 5609806a
  用户名    @ma6254
  手机号    +861****364
  状态      authed
  会员      否
  session   ~/.tg_down/sessions/mtproto-8613754320364.json
  创建时间  2026-11-19 00:10
  更新时间  2026-11-19 00:10

在线资料
  账号        9a6c 5609806a
  用户名      @ma6254
  手机号      +861****364
  生日        1999-02-07
  简介        -

已登录客户端（2）
  * Telegram Desktop · PC · Hong Kong · 最近活跃 2026-11-18 23:57
    Telegram Android · Android · Xiaomi 14 Pro · ... · 最近活跃 2026-11-18 22:31

  * 表示当前会话
```

`switch` 只接受 `account list` 第一列显示的**账号 ID**（纯数字），切换前会确认该账号的
session 文件存在；手机号、session 文件名等写法不再支持。选择结果保存在数据库的
`settings.current_account_id`，`list` 会用 `*` 标出当前账号。

```
$ tg_down account switch 3
已切换当前账号
  ID       3
  名称     主号
  session  mtproto-8613800000000.json

$ tg_down account list

本地账号（1）
     ID   类型     名称   手机号         状态     session
  *  3    mtproto  主号   +861****000   authed   mtproto-8613800000000.json

  * 表示当前账号（account switch 切换）
```

`list` 只读本地数据库（`~/.tg_down/data.db` 的 `accounts` 表），由 `account login` / `--probe`
写入，不联网、没有额外参数。

需要查看本机 Telegram Desktop 的账号（以及已登录设备）时，用 `account login --probe`。

`logout` 参数：

| 参数 | 说明 |
|---|---|
| `<账号ID\|名称>` | 二选一：纯数字按 ID，其余按展示名 / 手机号 |
| `--all` | 删除全部账号，不能与目标同时指定 |
| `--yes` | 跳过交互确认（不确认时会提示输入 yes） |

退出登录会删除数据库记录与对应 session 文件，不可恢复；已下载的文件不受影响。

> 当前状态：`login`（mtproto）、`list`、`switch`、`logout` 均已接入 SQLite；`botapi` 登录尚未接入。

## 数据目录与数据库

运行数据默认放在 `~/.tg_down/`（Windows：`C:\Users\<你>\.tg_down\`），首次运行时自动创建（权限 `0700`）：

```
~/.tg_down/
  data.db      # SQLite：accounts（账号）+ settings（系统配置）
```

- 用环境变量 `TG_DOWN_HOME` 可以改目录，例如指向同步盘或 U 盘。
- 数据库使用纯 Go 驱动（`glebarez/sqlite` → `modernc.org/sqlite`），**不需要 CGO / gcc**。
- `accounts` 表字段：`id / kind / name / phone / username / session_path / state / last_error / created_at / updated_at`。
- `settings` 表字段：`key / value / updated_at`。
- session 文件本身存放在 `<数据目录>/sessions`（默认 `~/.tg_down/sessions/`，位置固定，只随 `TG_DOWN_HOME` 变化），
  数据库只记录**相对数据目录**的路径（如 `sessions/mtproto-xxx.json`），整个数据目录搬走后仍然有效；
  token、session 内容不写入数据库。

## 系统配置

配置存在数据库的 `settings` 表，用 `config` 命令读写：

```bash
tg_down config list                     # 列出全部配置（别名 ls）
tg_down config get http_proxy           # 读取单项
tg_down config set http_proxy http://127.0.0.1:7897
tg_down config unset http_proxy         # 删除（别名 delete / remove）
tg_down config export                   # 导出为 YAML（默认输出到终端）
tg_down config export -f json
tg_down config export -f toml -o config.toml
tg_down config import config.yaml           # 按扩展名自动识别格式，合并恢复
tg_down config import config.toml --replace # 用文件重置已知配置项
tg_down config import backup.json -f json   # 扩展名不对时手动指定
```

当前支持：

| 键 | 说明 |
|---|---|
| `http_proxy` | 出站代理，支持 `http://`、`https://`、`socks5://`、`socks5h://`；登录与下载都会使用 |
| `http_proxy_enabled` | 布尔值，默认 `true`；`false` 时保留 `http_proxy` 但强制直连 |
| `current_account_id` | 当前选中的账号 ID，由 `account switch` 维护（手工设置需填已存在的账号 ID） |

> 配置项的定义、校验与展示脱敏集中在 `internal/config` 包，命令层只负责读写与输出；
> 新增配置项只需在 `config.items` 里登记一条。
>
> 导入导出能力是库函数：`Store.ExportJSON()` / `ExportYAML()` / `ExportTOML()` /
> `Store.Export(Format)`，以及 `Store.Import(Format, data)` / `Store.Replace(Format, data)`。
> TOML 由内置编码器输出（稳定排序、合规转义），导入使用 `github.com/BurntSushi/toml` 解码；
> YAML 使用 `gopkg.in/yaml.v3`。
>
> 导入语义：默认**合并**，只覆盖文件中出现的配置项；`--replace` 会先用文件重置已知配置项
> （文件中缺失的已知项被删除）。文件里未登记的键会被跳过并在输出里列出。

代理只从配置读取：`http_proxy_enabled` 为 true（默认）且配置了 `http_proxy` 时登录与下载都走它，否则直连；没有对应的命令行参数。

服务启动时如果 `settings` 表为空，会写入 `http_proxy_enabled=true` 作为默认配置；
`http_proxy` 的默认值是空串（未配置），不会写入空行。

- 设置时会校验协议与主机，非法值直接拒绝。
- `config list` / `config get` 输出时会隐藏代理密码（`http://user:***@host`）。
- 目前 `config.json` 文件尚未接入；数据库配置是当前生效的那一层。

## 配置（规划中）

优先级：命令行参数 > 环境变量（`TG_DOWN_*`）> `config.json` > 内置默认值。

> 注：配置文件与环境变量尚未接入，当前仅命令行参数与数据库 `settings` 生效。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `server.http_addr` | `127.0.0.1:22429` | 监听地址，默认仅本机 |
| `download.dir` | `./downloads` | 下载根目录 |
| `download.max_concurrency` | `1` | 并发下载数 |
| `auth.token` | 空 | 对外暴露时必填 |
| `botapi.base_url` | 官方地址 | 指向自建 local bot-api-server |

## 相关文档

- [整体规划](./doc/plan.md)
- [API 草案](./doc/api.md)
- [参考实现分析](./doc/reference.md)

## 许可证

见 [LICENSE](./LICENSE)。当前文件为空，待补充。
