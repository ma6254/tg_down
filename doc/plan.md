# tg_down 规划

Telegram 下载工具：Golang 后端 + React/TypeScript 前端。单用户，本地优先，支持 MTProto 账号与 Bot API 两种接入。

> 决策来源：用户确认「A、B 都要；单用户，暴露时加 token；前端 React + TS + Tailwind」。

## 1. 目标与范围

### MVP 要做
- 账号接入：MTProto 手机号登录（验证码 / 两步密码 / QR 可选）、Bot Token 接入（getMe 校验）。
- 浏览：按账号列会话（频道 / 群 / 私聊），按会话列消息，筛出含媒体的消息。
- 下载：单文件下载、进度实时显示、暂停 / 继续 / 重试 / 删除、断点续传、落盘重命名与去重。
- 任务：并发队列、低并发限流、`FLOOD_WAIT` 自动挂起并定时恢复。
- 部署：单二进制（`go:embed` 前端），默认 `127.0.0.1:22429`；远程访问时开启 Bearer token。

### MVP 不做（后续再说）
- 多用户 / 注册 / 权限分级。
- 在线预览播放器、消息发送、批量导出。
- 定时任务、自动订阅新消息。
- 非 Telegram 源（磁力、HTTP）。

## 2. 双后端选型

| 维度 | A. MTProto 用户账号（gotd/td） | B. Bot API（官方 HTTP） |
|---|---|---|
| 能下什么 | 账号能看到的任意聊天里的媒体 | 只有 bot 自己收到/所在会话可见的文件 |
| 单文件上限 | 2GB（Premium 4GB） | 20MB（自建 local bot-api-server 可到 2GB） |
| 鉴权 | 手机号 + 验证码 + 2FA，session 需持久化 | Bot Token |
| 风控 | 有封号风险，必须低并发 | 相对稳定，但受官方限流 |
| 典型用途 | 抓频道/群的历史媒体 | 自己的机器人转发仓库 |

结论：**两条都实现**，抽象为同一套 `tgclient` 接口，UI 上用"账号"统一呈现。MTProto 是主力，Bot API 作为稳定、低风险的补充通道。

### 统一抽象

```go
// internal/tgclient/client.go
type Kind string // "mtproto" | "botapi"

type Account struct {
    ID    int64  // 本地自增
    Kind  Kind
    Name  string // 展示名 / bot username / 手机号掩码
    State string // "unauthed" | "authed" | "error"
}

type Chat struct {
    ID       int64
    Title    string
    Username string
    Kind     string // channel / group / user
}

type Media struct {
    MessageID int
    FileName  string
    Size      int64
    MIME      string
    Kind      string // photo / video / audio / document
    // 后端各自需要的定位信息
    FileID    string // Bot API 用
    AccessHash int64 // MTProto 用
    DCID      int
}

type Client interface {
    Me(ctx context.Context) (Account, error)
    Chats(ctx context.Context, limit int) ([]Chat, error)
    Messages(ctx context.Context, chatID int64, offset, limit int) ([]Media, error)
    // 从 offset 开始读，写进 w，返回写入字节数；实现方负责分块与限流
    Fetch(ctx context.Context, m Media, offset int64, w io.Writer, onChunk func(int64)) error
}
```

- `internal/tgclient/mtproto`：gotd/td，单 `*tg.Client` + session 文件。
- `internal/tgclient/botapi`：`net/http` 调 Bot API；下载走 `getFile` → `/file/bot<token>/<file_path>`（本地模式下换成 `localhost:8081`）。`file_path` 会过期，**每次下载前重新 getFile**。
- 工厂：`tgclient.Open(account)` 按 `Kind` 返回对应实现。

## 3. 总体架构

```
┌──────────────── React (Vite + TS + Tailwind) ────────────────┐
│  登录 / 账号 / 会话 / 消息 / 任务 / 设置                      │
└───────────────┬──────────────────────────────────────────────┘
                │ REST (JSON) + SSE (进度)
┌───────────────▼──────────────── Go 单进程 ───────────────────┐
│  httpapi       路由、中间件(token)、handler、SSE Hub          │
│  account       账号注册、登录流程编排、session 生命周期        │
│  tgclient      mtproto | botapi 两个 adapter                  │
│  download      Manager、worker pool、断点续传、限流器          │
│  store         SQLite（任务、账号、设置）                      │
└───────────────┬──────────────────────────────────────────────┘
                │
        ┌───────▼────────┐      ┌────────────────────┐
        │ 磁盘 .part→最终 │      │ Telegram (MTProto) │
        └────────────────┘      │ / Bot API / local  │
                                └────────────────────┘
```

要点：
- **单进程单二进制**：`//go:embed` 打包 `tg_down_web/dist`，部署只拷贝一个 exe。
- **开发期分离**：Vite dev server `proxy /api → 127.0.0.1:22429`，HMR 正常。
- **状态真相在 SQLite**：内存只放 worker 与进度，重启后从库恢复任务。

## 4. 目录结构

```
tg_down_server/
  cmd/tgdown/main.go        # 启动、flag/env、优雅退出
  internal/config/          # 端口、下载目录、并发、代理、token
  internal/account/         # 账号 CRUD、登录状态机
  internal/tgclient/        # Client 接口 + 工厂
    mtproto/                # gotd/td 实现（登录、会话、消息、下载）
    botapi/                 # Bot API 实现
  internal/download/        # Manager、worker、resume、limiter、进度 Hub
  internal/store/           # SQLite（modernc.org/sqlite，纯 Go 免 CGO）
  internal/httpapi/         # router、handlers、sse、token 中间件
  webembed/embed.go         # //go:embed 前端产物
  go.mod

tg_down_web/
  src/
    api/                    # fetch 封装、SSE 订阅
    pages/                  # Login / Accounts / Chats / Downloads / Settings
    components/             # MediaCard、TaskRow、ProgressBar...
    hooks/                  # useSse、useTasks
    store/                  # 轻量全局状态（账号切换、筛选）
  vite.config.ts            # proxy /api
  tsconfig.json

tg_down_server/doc/
  README.md  plan.md  api.md  reference.md
```

## 5. 数据模型（SQLite）

```sql
CREATE TABLE accounts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  kind         TEXT NOT NULL,          -- mtproto | botapi
  name         TEXT NOT NULL,
  phone        TEXT,
  token        TEXT,                  -- bot token（本地库，需权限保护）
  session_blob BLOB,                  -- gotd session（敏感）
  state        TEXT NOT NULL,         -- unauthed | authed | error
  last_error   TEXT,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);

CREATE TABLE downloads (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id   INTEGER NOT NULL,
  chat_id      INTEGER NOT NULL,
  chat_title   TEXT,
  message_id   INTEGER NOT NULL,
  file_name    TEXT NOT NULL,
  file_size    INTEGER NOT NULL,
  mime         TEXT,
  media_kind   TEXT,
  status       TEXT NOT NULL,         -- pending | running | paused | done | failed
  offset       INTEGER NOT NULL DEFAULT 0,
  temp_path    TEXT,
  save_path    TEXT,
  speed        INTEGER DEFAULT 0,     -- 仅运行时有意义
  retry_at     INTEGER,               -- FLOOD_WAIT 定时恢复
  error        TEXT,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  UNIQUE(account_id, chat_id, message_id)
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

- 去重靠 `UNIQUE(account_id, chat_id, message_id)`：重复入队直接返回已有记录。
- `offset` 每 1–2 秒或每 4MB 落库一次，重启即可续传。
- Bot API 的文件建议再加 `file_unique_id` 去重（同一文件可能通过不同消息反复出现）。

## 6. 核心模块设计

### 6.1 账号与登录
- MTProto：`sendCode → signIn → (2FA) checkPassword`，三步分别对应 API；session 用 `session.Storage` 落 SQLite 或独立文件，退出登录时清空并删 session。
- Bot API：输入 token → `getMe` 校验 → 存库；失败给出明确错误（401/404 区分 token 错与网络错）。
- 账号状态机：`unauthed → (登录中) → authed | error`，前端轮询或 SSE 推 `account.state`。

### 6.2 下载管理器
- 全局 worker pool，默认并发 3（MTProto 上限建议 2–3；Bot API 可略高，但统一从配置读）。
- 每个任务：
  1. 校验目标路径在下载根目录内（`filepath.Clean` + `filepath.IsLocal`）。
  2. 若 `.part` 存在则从 `offset` 续传。
  3. `Fetch` 分块写入，`onChunk` 更新内存进度 + 周期落库。
  4. 完成后 `os.Rename` 到最终名（同名自动 ` (1)` 后缀）。
- 失败分类：
  - `FLOOD_WAIT_X` → 置 `paused` + `retry_at = now + X + 5s`，到点由调度器唤醒。
  - 可重试错误（网络、超时、5xx）→ 指数退避，最多 N 次。
  - 不可重试（文件过大、权限、路径非法）→ `failed`，错误原样返回前端。

### 6.3 进度推送
- SSE：`GET /api/v1/events`，服务端 500ms 聚合一次运行中任务（id、offset、size、speed），逐条 `data: {...}\n\n`。
- 前端 `EventSource` 订阅，只做渲染；断线自动重连并按 `GET /api/v1/downloads` 全量对齐。
- 不引入 WebSocket：单向推送足够，零额外依赖。

### 6.4 文件命名
- 清洗 `<>:"/\|?*` 与控制字符，去首尾空格与点，限制 180 字符。
- 无文件名时用 `{chat_title}_{message_id}.{ext}`，ext 由 MIME 推断。
- 落盘目录按账号/会话分：`<root>/<account_name>/<chat_title>/`（可配置平铺）。

## 7. 认证与安全

单用户模型，但必须"本地默认安全、暴露时才加锁"：

| 配置 | 行为 |
|---|---|
| `--listen 127.0.0.1:22429`（默认） | 不要求 token，本地直接可用 |
| `--listen 0.0.0.0:22429` | 启动时若未配置 token，**拒绝启动**并提示 |
| `--auth-token <t>` 或 `TG_DOWN_TOKEN` | 所有 `/api/*` 要求 `Authorization: Bearer <t>` |
| 首次对外启动 | 可自动生成随机 token 并打印到控制台 |

- token 校验用常量时间比较（`crypto/subtle`）。
- 前端登录页只填 token，存 `localStorage`，401 时清空回登录页。
- session blob / bot token 属于高敏数据：库文件权限收紧，接口一律不回传明文；退出登录清空。
- 下载路径校验统一走 `httpapi` 与 `download` 双重检查，杜绝 `../` 穿越。
- 建议默认开启请求日志（不含 token），便于排查 FloodWait。

## 8. 前端规划

技术栈：React 19 + TypeScript + Vite + Tailwind CSS + React Router + TanStack Query（服务端状态）+ 少量 zustand（账号切换、UI 偏好）。

| 页面 | 内容 |
|---|---|
| `/login` | token 输入（仅远程模式出现） |
| `/accounts` | 账号列表、新增（MTProto 三步向导 / Bot Token）、状态、退出登录 |
| `/chats` | 账号下拉 + 会话列表（搜索、按类型过滤） |
| `/chats/:id` | 消息流、媒体卡片（大小、类型、下载按钮）、多选批量下载 |
| `/downloads` | 任务表：进度条、速度、剩余时间、暂停/继续/重试/删除、打开所在目录 |
| `/settings` | 下载目录、并发数、代理（SOCKS5 / MTProxy）、命名策略、token 管理 |

- 长列表（消息/任务）用虚拟滚动。
- 进度条只接受 SSE 增量，避免整表重渲染：行级 memo。
- 移动端响应式：Tailwind 断点，任务页在窄屏转为卡片列表。

## 9. 里程碑

| 阶段 | 内容 | 预估 |
|---|---|---|
| M0 | 三目录脚手架、Go 路由与中间件、SQLite 建表、Vite 代理、`go:embed` 跑通 | 0.5–1 天 |
| M1 | 账号体系：Bot Token 接入 + MTProto 登录（验证码/2FA）、session 持久化 | 2 天 |
| M2 | 会话与消息浏览接口 + 前端 Chats/消息页（双后端） | 2 天 |
| M3 | 单文件下载 + `.part` 续传 + SSE 进度 | 2 天 |
| M4 | 任务队列、并发限流、FloodWait 挂起恢复、失败重试 | 1–2 天 |
| M5 | 任务页 / 设置页 / 批量下载 / 错误提示打磨 | 2 天 |
| M6 | 单二进制打包、Windows 路径与权限处理、README、Dockerfile（可选） | 1 天 |

合计约 10–12 个工作日可到可用 MVP。

> M0–M2 可基于已有项目 `omni_toolbox` 的骨架改造（同为本机单用户 Go + React 嵌入式应用），复用清单与迁移顺序见 [reference.md](./reference.md)，预计可省 2–3 天。

## 10. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| MTProto 账号被封 | 工具不可用 | 用小号；并发 ≤3；不频繁拉历史；不在登录态做批量操作 |
| 单文件 2GB/4GB 上限 | 大文件无法下载 | 入队前校验 size，UI 明确提示"超出 MTProto 限制" |
| `FLOOD_WAIT` | 队列整体阻塞 | 一开始就按"可挂起可恢复"设计，失败不重试而是排队等待 |
| Bot API 20MB 限制 | 大文件下载失败 | 文档说明；或引导自建 local bot-api-server（2GB） |
| 本地 bot-api-server 部署复杂 | 用户放弃 | 作为可选项，配置里给 `botapi.base_url` 覆盖 |
| CGO / Windows 交叉编译 | 打包麻烦 | 用 `modernc.org/sqlite`（纯 Go） |
| session/token 泄露 | 账号被盗 | 库文件权限收紧、接口不回传、默认只监听回环 |
| 文件名与路径注入 | 写到根目录外 | 统一 sanitize + `filepath.IsLocal` 校验 |

## 11. 待定 / 后续

- 是否需要 QR 登录（MTProto）。
- 是否需要"下载后自动转码/压缩"（超出 MVP，单列）。
- 代理方式：SOCKS5 足够，MTProxy 是否要支持取决于部署环境。
- 是否提供 Docker 镜像（若用户主要在 NAS 上跑，建议做）。
