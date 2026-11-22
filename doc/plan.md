# tg_down 规划

Telegram 下载工具：Go 后端 + React/TypeScript 前端。单用户、本地优先；默认回环监听，需要时用 Bearer token 对外暴露。

> **最后更新：2026-11-22**。本文按「现状 — 规划 — 路线图 — 风险」组织。
> 代码与 [README](../README.md)、[API 文档](./api.md) 是事实来源；本文中的「规划」部分尚未实现。
> 决策来源：用户确认「MTProto 与 Bot API 都要；单用户；暴露时加 token；前端 React + TS + Tailwind」。

## 0. 导读

### 0.1 状态图例

| 标记 | 含义 |
|---|---|
| ✅ 已实现 | 代码已落地，有接口/命令可用 |
| 🚧 规划中 | 已设计、未实现；文中会标注 |
| ⏸ 暂不做 | 已确认排除，后续需要再单独立项 |

### 0.2 当前状态

| 领域 | 状态 | 说明 |
|---|---|---|
| MTProto 账号 | ✅ | CLI 登录、tdata 探测导入、切换、详情、在线资料、头像、登出/删除 |
| Bot API 账号 | 🚧 | 无客户端实现；`--botapi-base-url` 仅预留 |
| 配置 | ✅ | `settings` 表 + `config` 命令 + Web 设置页 |
| 链接解析 / 预览 | ✅ | `POST /links/preview`，支持正文、链接、媒体、相册 |
| 下载任务 | ✅ | 队列、`.part` 续传、暂停/继续/重试、SSE、缩略图 |
| 会话界面 | 🚧 | 群/频道/私聊/收藏夹，仿 Telegram Desktop；M2 |
| 群 / 频道监控 | 🚧 | 新消息过滤后自动下载；M7–M9 |
| RSS 输出 | 🚧 | 每个监控生成 RSS 2.0 feed；M10–M11 |
| 下载页草稿 | 🚧 | 输入 + 上次解析摘要存数据库；M12 |
| 下载输入历史 | 🚧 | 最近 20 条解析批次；M13 |
| QR / 转码 / MTProxy / Docker | ⏸ | 见 §3.6 |

### 0.3 文档结构

| 章节 | 内容 |
|---|---|
| §1 | 产品目标与范围 |
| §2 | 现状：架构、目录、数据模型、核心模块、安全 |
| §3 | 规划：会话界面、监控、RSS、草稿、历史、暂不做 |
| §4 | 路线图：里程碑与依赖 |
| §5 | 风险与对策 |

## 1. 产品范围

### 1.1 目标

- **单用户、本地优先**：不做多租户、不注册；默认只监听 `127.0.0.1`。
- **MTProto 为主**：用账号能看到的内容下载媒体；Bot API 作为后续补充通道。
- **任务化下载**：队列、并发限制、断点续传、暂停/继续/重试、失败可读。
- **只读浏览**：群 / 频道 / 私聊 / 收藏夹查看，仿 Telegram Desktop；不发送、不转发、不删除。
- **自动化**：群 / 频道监控 + RSS 输出，把「看到新内容」变成「自动下载 / 订阅」。
- **单二进制**：前端 `go:embed` 进可执行文件，部署只拷贝一个文件。

### 1.2 范围划分

**已实现**

- 账号：MTProto CLI 登录、tdata 探测导入、切换、详情、在线资料、头像、登出/删除。
- 配置：`http_proxy`、`api_id` / `api_hash` / `api_custom_enabled`、`current_account_id`。
- 链接解析与预览：正文、富文本实体、内嵌链接、媒体/相册元数据、缩略图。
- 下载：链接/相册入队、覆盖策略、并发、续传、控制、SSE 进度、任务缩略图。
- Web：账号页、设置页、下载页；单二进制构建。

**规划中**

- 会话界面（M2a–M2c）：群 / 频道 / 私聊 / 收藏夹，真实文件夹/归档，手动标记已读。
- 群 / 频道监控（M7–M9）：新消息过滤后自动入队。
- RSS 输出（M10–M11）：每个监控生成 RSS 2.0 + Media RSS feed。
- 下载页草稿（M12）与输入历史（M13）。

**暂不做**

- 多用户 / 注册 / 权限分级；消息发送、在线播放器、批量导出、定时任务。
- 非 Telegram 源（磁力、HTTP）；QR 登录；下载后转码/压缩；MTProxy；Docker。
- 详见 §3.6。

## 2. 现状

### 2.1 总体架构

```
┌──────────────── React (Vite + TS + Tailwind) ────────────────┐
│  登录 / 账号 / 会话 / 消息 / 任务 / 设置 / 监控 / RSS（规划） │
└───────────────┬──────────────────────────────────────────────┘
                │ REST (JSON) + SSE (进度)
┌───────────────▼──────────────── Go 单进程 ───────────────────┐
│  server        路由、中间件(token)、handler、SSE Hub          │
│  rss           RSS 2.0 输出与媒体直链（规划）                 │
│  account       账号注册、登录流程编排、session 生命周期        │
│  tgclient      mtproto adapter（botapi 未实现）               │
│  download      Manager、worker pool、断点续传、限流器          │
│  monitor       群/频道监控 supervisor、过滤、自动入队（规划）  │
│  store         SQLite（任务、账号、设置）                      │
└───────────────┬──────────────────────────────────────────────┘
                │
        ┌───────▼────────┐      ┌────────────────────┐
        │ 磁盘 .part→最终 │      │ Telegram (MTProto) │
        └────────────────┘      │ / Bot API / local  │
                                └────────────────────┘
```

要点：

- **单进程单二进制**：`//go:embed` 打包 `webembed/dist.zip`，部署只拷贝一个 exe。
- **常驻进程只有 `serve`**：HTTP、下载队列、规划中的监控与 RSS 都在 `serve` 内运行；其他子命令做一次性操作或配置管理。
- **开发期分离**：Vite dev server `proxy /api → 127.0.0.1:22429`，HMR 正常。
- **状态真相在 SQLite**：内存只放 worker 与进度，重启后从库恢复任务。

### 2.2 目录结构

```
tg_down_server/
  main.go                   # 命令入口（cobra 根命令）
  cmd/                      # serve / account / config / download / version；monitor 规划中
  internal/appdir/          # 数据目录解析（exe 目录 / TG_DOWN_HOME）
  internal/config/          # 配置项定义、校验、脱敏
  internal/database/        # SQLite（GORM + glebarez/sqlite，纯 Go 免 CGO）
  internal/account/         # 账号 CRUD、登录状态机、tdata 探测
  internal/tgclient/
    mtproto/                # gotd/td 实现（登录、解析、缩略图、下载）
  internal/link/            # Telegram 链接解析
  internal/download/        # Manager、worker、resume、limiter、进度
  internal/monitor/         # 群/频道监控 supervisor、过滤与去重（规划中）
  internal/rss/             # RSS 2.0 渲染、feed token 与媒体直链（规划中）
  internal/avatar/          # 账号 / peer 头像缓存
  internal/thumbcache/      # 媒体缩略图缓存
  internal/server/          # router、handlers、sse、token 中间件、Web 托管
  webembed/                 # //go:embed dist.zip（-tags webdist 时启用）
  build.py
  go.mod

tg_down_web/
  src/
    api/                    # fetch 封装、SSE 订阅、DTO
    pages/                  # Login / Accounts / Downloads / Settings
    components/             # 布局、账号、消息文本、图片查看器、ui
    hooks/                  # useAuth、useAccounts、useDownloads、usePreviewLinks
    lib/                    # 格式化、脱敏、查询客户端、主题
  vite.config.ts            # proxy /api
  tsconfig.json

tg_down_server/doc/
  README.md  plan.md  api.md  reference.md
```

### 2.3 双后端选型（历史决策）

| 维度 | A. MTProto 用户账号（gotd/td） | B. Bot API（官方 HTTP） |
|---|---|---|
| 能下什么 | 账号能看到的任意聊天里的媒体 | 只有 bot 自己收到/所在会话可见的文件 |
| 单文件上限 | 2GB（Premium 4GB） | 20MB（自建 local bot-api-server 可到 2GB） |
| 鉴权 | 手机号 + 验证码 + 2FA，session 需持久化 | Bot Token |
| 风控 | 有封号风险，必须低并发 | 相对稳定，但受官方限流 |
| 典型用途 | 抓频道/群的历史媒体 | 自己的机器人转发仓库 |

结论：两条都保留，UI 上用「账号」统一呈现；MTProto 是主力，Bot API 作为稳定、低风险的补充通道。

> 落地情况：没有抽出统一的 `Client` 接口；`internal/tgclient` 目前只有 `mtproto` 实现，下载与链接解析直接调用 `mtproto.Session`。Bot API 尚未实现。

### 2.4 数据模型（SQLite）

模型定义在 `internal/database`，由 GORM AutoMigrate 建表。**已实现**：

| 表 | 关键字段 | 说明 |
|---|---|---|
| `accounts` | `id / kind / source / name / phone / username / session_path / state / last_error / tg_user_id / created_at / updated_at` | session 以文件保存，表里只存相对路径；`tg_user_id` 唯一 |
| `settings` | `key / type / value / updated_at` | 配置项；`type` 为 `string / integer / boolean` |
| `download_tasks` | `id / link / account_id / chat_id / chat_title / message_id / file_name / file_size / kind / mime / status / offset / speed / error / save_path / overwrite / thumbnail_url / preview_url / thumb_key / preview_key / created_at / updated_at` | `link` 唯一，重复入队复用任务 |

规则：

- 账号表不存 `token` / `session_blob`；session 文件权限收紧到 `0600`。
- `offset` 每 1–2 秒或每 4MB 落库一次，重启即可续传。
- Bot API 接入后建议再加 `file_unique_id` 去重。

**规划中**：

| 表 | 用途 | 章节 |
|---|---|---|
| `ui_drafts` | 界面输入草稿（当前仅 `downloads`） | §3.4 |
| `download_inputs` | 下载页历史输入 + 摘要缓存 | §3.5 |
| `peer_messages` | 监控消息归档，供 RSS 与调试 | §3.3 |
| `rss_feeds` | feed token、mode、enclosure、limit | §3.3 |
| `monitors` / `monitor_matches` | 监控配置、命中与跳过记录 | §3.2 |

### 2.5 核心模块

#### 2.5.1 账号与登录

- MTProto：`sendCode → signIn → (2FA) checkPassword`；session 用独立文件保存（`<数据目录>/sessions/`），数据库只记录相对路径，退出登录时删除文件与记录。
- 当前 HTTP 登录三步尚未实现，只有 CLI `account login` 与 `POST /accounts/probe` 两条接入路径。
- Bot API：输入 token → `getMe` 校验 → 存库；区分 token 错误与网络错误。（**未实现**）
- 账号状态机：`unauthed → (登录中) → authed | error`；`account.state` 不通过 SSE 推送，前端按需重新拉取。
- 内置 desktop 应用凭据（api_id `2040`）零配置可用；`--app custom` 使用自备凭据。

#### 2.5.2 下载管理器

- 全局 worker pool，默认并发 1（`--max-concurrency`，范围 1–8；MTProto 建议不超过 3）。
- 每个任务：
  1. 校验目标路径在下载根目录内（`filepath.Clean` + `filepath.IsLocal`）。
  2. 若 `.part` 存在则从 `offset` 续传。
  3. 分块写入，`onChunk` 更新内存进度并周期落库。
  4. 完成后 `os.Rename` 到最终名（同名自动加 ` (1)` 后缀，`overwrite` 时直接替换）。
- 失败分类：
  - `FLOOD_WAIT_X` → 设计为 `paused` + `retry_at`，到点唤醒；**当前未实现**，任务会 `failed`，需手动重试。
  - 可重试错误（网络、超时、5xx）→ 设计为指数退避；**当前未实现**，失败后停在 `failed`。
  - 不可重试（文件过大、权限、路径非法）→ `failed`，错误原样返回前端。
- 删除运行中任务会先取消 context；`.part` 总是清理，最终文件只在 `delete_file=true` 时删除。

#### 2.5.3 进度推送

- SSE：`GET /api/v1/events`，服务端约 1.5 秒聚合一次运行中任务（id、offset、size、speed）。
- 前端用 `fetch` 读流（`EventSource` 无法带 `Authorization` 头），断线指数退避重连，并按 `GET /downloads` 全量对齐。
- 事件：`task.update`、`task.progress`、`task.deleted`、`ping`；慢客户端丢中间事件，不拖住下载。
- 不引入 WebSocket：单向推送足够。

#### 2.5.4 文件命名

- 清洗 `<>:"/\|?*` 与控制字符，去首尾空格与点，限制 180 字符。
- 无文件名时用 `{chat_title}_{message_id}.{ext}`，ext 由 MIME 推断。
- 落盘目录按会话分：`<root>/<chat_title>/`；没有账号层，暂不支持平铺配置。

### 2.6 认证与安全

单用户模型，但「本地默认安全、暴露时才加锁」：

| 配置 | 行为 |
|---|---|
| `--listen 127.0.0.1:22429`（默认） | 不要求 token，本地直接可用 |
| `--listen 0.0.0.0:22429` | 启动时若未配置 token，**拒绝启动**并提示 |
| `--auth-token <t>` | 所有 `/api/*` 要求 `Authorization: Bearer <t>`（没有环境变量入口） |
| 首次对外启动 | **未实现自动生成**：必须显式提供 `--auth-token`，否则拒绝启动 |

- token 校验用常量时间比较（`crypto/subtle`）。
- 前端登录页只填 token，存 `localStorage`，401 时清空回登录页。
- session 文件与 bot token 属于高敏数据：接口不回传内容；bot token 尚未接入，接入后同样不能写日志或回传。
- 下载路径校验统一走 `server` 与 `download` 双重检查，杜绝 `../` 穿越。
- `/rss/*`（规划）不走 Bearer 中间件，靠 feed token 鉴权。
- 默认开启请求日志（不含 token），便于排查 FloodWait。

## 3. 规划

> 以下功能均未实现。已确认的默认值写在各自「已确认」小节，接口细节见 [api.md](./api.md)。

### 3.1 会话界面（仿 Telegram Desktop，规划）

#### 3.1.1 页面与路由

| 页面 | 内容 | 状态 |
|---|---|---|
| `/login` | token 输入（仅远程模式出现） | ✅ |
| `/accounts` | 账号列表、tdata 导入、切换、详情、在线资料、登出/删除 | ✅（MTProto 三步向导与 Bot Token 入口未实现） |
| `/chats` | 群 / 频道 / 私聊 / 收藏夹列表 | 🚧 M2 |
| `/chats/:peer` | 消息流；`peer` 形如 `channel:3713824040`、`user:355927778` | 🚧 M2 |
| `/downloads` | 链接解析预览 + 任务表 | ✅ |
| `/settings` | 服务信息、配置项 | ✅（下载目录/并发/命名策略未登记为配置项） |

#### 3.1.2 布局与会话列表

- 桌面（≥ 1280px）：应用侧栏（15rem）+ 会话列表（22rem）+ 消息区 + 信息面板（20rem，可收起）。
- 1024–1280px：信息面板改为抽屉，保留「侧栏 + 列表 + 消息」。
- < 1024px：应用侧栏收进 Sheet；`/chats` 只显示列表，`/chats/:peer` 只显示对话，顶部返回。
- 保留现有应用侧栏，不照搬 Telegram Desktop 的窄导航栏；聊天区内部布局与交互对齐 Desktop。
- 顶部页签来自 `GET /chats/folders`：全部、归档、自定义文件夹（带 emoticon）；收藏夹作为会话行显示，不单独做页签。
- 不单独做「群组 / 频道 / 私聊」页签；类型在会话行与详情里展示，搜索可过滤。
- 会话行：高 72px、头像 54px 圆形；标题、最后一条消息预览（群组带发送人前缀）、时间、置顶/静音图标、未读徽章、选中态。
- 排序：置顶优先，其余按最后消息时间倒序；收藏夹固定在列表中显示为「收藏夹」。
- 搜索：一期本地过滤已加载会话，`Ctrl/Cmd+K` 聚焦；空/加载/错误/无结果状态分开。
- 未读徽章只展示，**默认不因查看而标记已读**；头部提供「标记已读」按钮。

#### 3.1.3 消息区

- 头部：头像、标题、副标题（群成员数 / 频道订阅数 / `@username` / 「收藏夹」）、搜索、刷新、标记已读、信息面板开关。
- 置顶条：当前置顶消息摘要，点击滚动到该消息；没有置顶时隐藏。
- 气泡：日期分隔、发送人分组（群组 5 分钟内同发送人合并，彩色名字 + 头像）、回复引用、转发来源、服务消息居中、时间/已编辑、频道浏览量。
- 文本渲染复用 `MessageText`（粗体/斜体/链接/代码/引用/emoji）；链接可复制或在 Telegram 打开。
- 媒体复用预览的 `media` / `medias`：图片/视频封面、时长、大小；相册 2–3 列网格；文档行；点击打开 `PicViewer` / `PicViewerMobile`；超限或不可下载时置灰并说明。
- 下载：单条悬浮「下载」；复选框 + Shift 范围多选，底部「下载选中 (n)」；一次最多 50 条，入队前二次确认。
- 私聊/收藏夹没有 `t.me` 链接，调用 `POST /downloads/from-messages`。
- 滚动：初始最近 50 条，向上加载更早；进入定位底部；「回到最新」浮钮；未读分隔线仅在开启已读策略时显示。
- 刷新：打开会话 15 秒轮询（一期）；监控的 updates 客户端接入后改 `chat.update` SSE（三期）。
- 只读：没有输入框、发送、转发、删除、回应按钮；提供复制文本、复制链接、在 Telegram 打开。

#### 3.1.4 信息面板与收藏夹

- 信息面板：头像、标题、类型、`@username`、简介、成员/订阅数、当前置顶消息；可收起。
- 「下载全部媒体」本期只渲染禁用按钮（提示「后续版本」），不实现批量入队。
- 收藏夹是 self 会话：标题显示「收藏夹」，消息 `out=true`、右对齐，不显示发送人分组。
- 私聊显示用户头像、`@username` 与最后一条消息；一期不做输入中/在线状态轮询。
- 频道帖子没有个人发送者时，显示频道名与 `post_author`（若有）。

#### 3.1.5 视觉与交互

- 信息层级、行高、气泡和页签对齐 Telegram Desktop，但不复制其图标、壁纸与素材。
- 颜色走主题变量：强调色浅色 `#3390ec` / 深色 `#8774e1`；选中行与未读徽章用强调色；incoming 气泡白/深灰，outgoing `#eeffde` / `#2b5278`（收藏夹全部 outgoing）。
- 圆角 10–12px，消息字号 15px、行高 1.35；聊天背景用低对比纯色或细纹理。
- 动画 120–150ms；`prefers-reduced-motion` 下关闭。
- 键盘：`Ctrl/Cmd+K` 聚焦搜索，`Esc` 清搜索或返回列表，`↑/↓` 移动会话，`Enter` 打开。
- 可访问性：会话列表 `role="listbox"`、消息区 `role="log"`；头像装饰性；按钮有可读标签；焦点可见。
- 消息列表用虚拟滚动（`@tanstack/react-virtual`）；超过 200 个会话时也虚拟化；图片懒加载、滚动位置保留、行级 `memo`。

#### 3.1.6 后端接口需求

| 接口 | 用途 | 备注 |
|---|---|---|
| `GET /accounts/{id}/chats?folder_id=&limit=&offset=` | 会话列表 | `last_message`、`unread_count`、`pinned`、`muted`、`self` |
| `GET /accounts/{id}/chats/folders` | 文件夹页签 | Telegram dialog filters + 归档 |
| `GET /accounts/{id}/chats/{peer}` | 会话信息 | 标题、简介、成员/订阅数、置顶消息 |
| `GET /accounts/{id}/chats/{peer}/messages?offset_id=&limit=&media_only=` | 消息分页 | 文本、实体、媒体、相册、回复、转发、服务消息 |
| `GET /accounts/{id}/chats/{peer}/pinned` | 置顶条 | 也可并入 chat info |
| `POST /accounts/{id}/chats/{peer}/read` | 手动标记已读 | 只在显式点击时调用 |
| `POST /downloads/from-messages` | 按 peer + message_id 入队 | 私聊/收藏夹必需；一次最多 50 条 |
| `GET /accounts/{id}/chats/{peer}/media` | 媒体 tab（二期） | 按类型分页 |
| `chat.update` SSE | 消息实时更新（三期） | 复用监控的 updates 客户端 |

- `{peer}` 用 `kind:id` 编码，避免用户/群/频道 ID 命名空间歧义；列表与消息响应同构。
- `folder_id`：`0` 全部、`1` 归档、`>=2` 自定义文件夹；会话列表默认 `0`。
- 媒体封面复用 `/media/thumbnail/<token>`；头像复用 `/accounts/{id}/avatar` 与 peer 头像地址。
- `unread_count` 只读；默认不自动已读，只有显式 `POST .../read` 会改变已读状态。
- 分页与批量拉取必须限流，避免翻历史触发 `FLOOD_WAIT`。

#### 3.1.7 分阶段

| 阶段 | 内容 |
|---|---|
| P1 只读基础 | 会话列表、真实文件夹/归档页签、消息流、会话信息、置顶条、单条下载、手动「标记已读」；15 秒轮询 |
| P2 媒体与批量 | 相册网格、多选批量下载（≤ 50 条）、信息面板（含占位按钮）、服务端搜索、媒体 tab |
| P3 实时与已读 | `chat.update` SSE、共享媒体聚合；自动已读仍不做 |

#### 3.1.8 已确认

1. 打开会话默认**不**标记已读；提供显式「标记已读」按钮，并提示会同步到其他客户端。
2. 支持真实 Telegram 文件夹与归档会话：页签来自 dialog filters + 归档。
3. 不显示在线状态/输入中，保持只读查看器。
4. 批量下载一次最多 50 条，入队前二次确认。
5. 「下载全部媒体」本期只放按钮占位，功能后续开发。
6. 移动端继续用现有侧栏 Sheet，不做底部 tab。

### 3.2 群 / 频道监控（规划）

#### 3.2.1 目标与场景

- 绑定「账号 + 群/频道」，持续检测新消息，命中规则后自动创建下载任务。
- 过滤条件可组合：媒体类型、发送人白/黑名单、是否群置顶、关键词、文件大小/时长、转发来源、相册。
- 触发方式：新消息、置顶动作，或两者。
- 可观测：监控状态、最近命中、跳过原因；提供「测试」在不创建任务的前提下回放最近消息。
- **运行位置**：由 `tg_down serve` 常驻执行，随 serve 启动与优雅退出；`monitor` 子命令只读写配置，没有独立守护进程。

典型场景：

- **资源群**：只下指定发布者的新视频，忽略常年置顶的合集帖（`pinned=exclude`）。
- **频道**：只下新视频；置顶帖作为索引，不重复下载。
- **置顶合集**：管理员把一条含多个视频的消息置顶时触发整组下载（`trigger=pin`）。

#### 3.2.2 Telegram 侧约束

| 约束 | 结论 |
|---|---|
| updates 只推给成员 | 群/频道必须先加入才能监控；不支持未加入的公开频道，也不自动加群 |
| 只支持已加入会话 | 创建/启用时校验成员身份，不是成员返回 409；不提供 `poll` 轮询模式 |
| 置顶有两层语义 | 「当前置顶」看 `ChannelFull.pinned_msg_id` / `ChatFull.pinned_msg_id`；「置顶动作」看 `updatePinnedMessages` 或 `messageActionPinMessage` |
| 发送人 | 普通群/超级群用 `from_id`；频道帖子没有个人发送者，只有频道本身与 `post_author`；匿名管理员/马甲可能落到 `sender_chat` |
| FloodWait | 监控是持续流量，必须做每监控速率上限与退避，不能只靠全局下载并发兜底 |

补充：

- 私有频道/群需要账号已有 access_hash；解析失败（`CHANNEL_INVALID` / `CHANNEL_PRIVATE`）时标记监控失效，等待重新解析或用户重新加入。
- 频道评论区（linked discussion）是另一个 peer：要监控评论，需把讨论群作为独立监控目标。

#### 3.2.3 触发与过滤

触发方式：

| trigger | 行为 |
|---|---|
| `new`（默认） | 新消息入库后评估 |
| `pin` | 消息被置顶时评估（含把旧消息置顶） |
| `both` | 两种都评估，依靠去重避免重复下载 |

置顶过滤 `pinned`：

| pinned | 含义 | 典型用途 |
|---|---|---|
| `any`（默认） | 不约束 | 普通新视频监控 |
| `only` | 只接受当前置顶消息 | 置顶视频合集、长期维护的索引帖 |
| `exclude` | 跳过当前置顶消息 | 避免反复命中常年置顶的旧帖 |

组合建议：

- 「管理员置顶就下载」：`trigger=pin` + `pinned=only`。
- 「新发的视频，若被置顶才下载」：`trigger=new` + `pinned=only`（需维护 pinned 缓存）。
- 「只下新视频，置顶旧帖不重复下」：`trigger=new` + `pinned=exclude`。

过滤条件（JSON，存 `monitors.filter_json`）：

```json
{
  "media": {"kinds": ["video"], "min_size": 0, "max_size": 0, "min_duration": 0, "max_duration": 0},
  "senders": {"include": ["user:123456", "@alice"], "exclude": ["@spam_bot"]},
  "pinned": "any",
  "text": {"include_any": ["4K", "完整版"], "exclude": ["预告", "宣传"]},
  "forward": {"exclude": false, "include_from": []},
  "album": "all"
}
```

求值顺序（先便宜后昂贵，`exclude` 优先）：

1. 媒体类型、大小、时长；没有可下载媒体直接跳过。
2. 发送人 include/exclude：`exclude` 命中即拒绝；`include` 非空时必须在名单内。
3. 转发来源。
4. 关键词 include/exclude（不区分大小写的子串匹配）。
5. 置顶状态（读监控缓存的 `pinned_message_id`，必要时刷新）。
6. 去重与速率限制。

过滤结果写进 `monitor_matches`（含 `reason` / `skipped_reason`），便于在 UI 里解释「为什么没下」。

#### 3.2.4 数据模型

`monitors`：

| 字段 | 说明 |
|---|---|
| `id` | 本地自增 |
| `account_id` | 绑定账号；同一 peer 可被不同账号分别监控 |
| `peer_kind` / `peer_id` / `access_hash` | 群/频道定位；`access_hash` 变化时重新解析 |
| `title` / `username` | 展示与链接拼接 |
| `trigger` | `new` / `pin` / `both` |
| `download_enabled` | 是否把命中消息入队下载；`false` 时只归档（供 RSS / 调试），默认 `true` |
| `filter_json` | 过滤条件 |
| `download_json` | 入队选项（`overwrite`、是否展开相册等） |
| `backfill_limit` | 启用时回溯的消息数，默认 0 |
| `last_message_id` | 已处理到的消息 ID（补扫游标） |
| `pinned_message_id` | 当前置顶消息缓存 |
| `enabled` / `status` / `last_error` / `last_event_at` | 运行状态 |
| `created_at` / `updated_at` | |

`monitor_matches`：

| 字段 | 说明 |
|---|---|
| `id` | 自增 |
| `monitor_id` / `peer_id` / `message_id` | 命中消息 |
| `sender_id` / `media_kind` / `file_name` / `file_size` | 展示与排查 |
| `task_id` | 创建的下载任务；跳过时为空 |
| `reason` / `skipped_reason` | 命中或跳过原因 |
| `matched_at` | Unix 秒 |

唯一键：`UNIQUE(monitor_id, peer_id, message_id)`，保证重连/补扫重复投递不会重复下载。

#### 3.2.5 运行时架构

```
每个账号一个常驻 updates client（NoUpdates=false）
  → tg.UpdateDispatcher / updates.Manager
  → monitor.Supervisor（按账号串行）
  → monitor.Filter（纯函数，可单测）
  → download.Manager.EnqueueMedia（直接入队，不再解析链接）
```

关键点：

- **生命周期**：监控 supervisor 在 `serve` 启动流程里、数据库迁移与下载管理器之后启动；没有启用监控时不建立 updates 连接。`serve` 收到退出信号时先停止 update 客户端与 supervisor，再关闭 HTTP 与下载管理器。
- **连接归属**：现有客户端都是短连接 + `NoUpdates: true`；监控需要长连接。目标是把「每账号一个 `mtproto.Conn`」抽出来，监控、预览、下载、缩略图共用；`updates.Manager` 用持久化 `StateStorage` + `AccessHasher`，重启后从 pts/seq 续传。MVP 可先让监控独占长连接，但同 session 文件被多个客户端写入有风险，**建议 M7 就把连接池做了**。
- **不入队链接**：监控已经拿到消息与媒体元数据，新增 `EnqueueMedia` 直接建 `download_tasks`，避免再走 `ResolveLinks` 的额外请求与二次限流。
- **补扫**：启动时从 `last_message_id` 拉 `messages.getHistory`（有上限），再转入实时；`updates.Manager` 报 `OnTooLong` / `OnChannelGap` 时也回退历史补扫，并把状态标为 `lagging`。
- **监控 CRUD 生效**：配置由 API/CLI 写入数据库，`serve` 进程内的 supervisor 定期对账（例如 5 秒），或在 CRUD 时写 `monitors_revision` 触发重载；数据库始终是唯一事实源。`serve` 未运行时配置照常保存，但不会产生监控流量。
- **去重**：`monitor_matches` 唯一键 + `download_tasks.link` 唯一，双保险。
- **限速**：每个监控独立令牌桶（默认 20 条/小时，可配）；单文件上限沿用下载器（普通 2 GB / Premium 4 GB）；遇到 `FLOOD_WAIT` 时该监控退避，不阻塞其他监控。

#### 3.2.6 API 草案

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/monitors` | 列表，含状态与最近命中数 |
| POST | `/api/v1/monitors` | 创建，body 见 api.md |
| GET/PUT/DELETE | `/api/v1/monitors/{id}` | 详情 / 修改 / 删除 |
| POST | `/api/v1/monitors/{id}/enable`、`/disable` | 启停 |
| POST | `/api/v1/monitors/{id}/test` | 干跑：只返回最近 N 条消息的过滤结果，不建任务 |
| GET | `/api/v1/monitors/{id}/matches` | 命中/跳过记录，分页 |
| POST | `/api/v1/monitors/resolve` | 解析链接或 `@username`，返回 peer 预览 |

- 未加入的群 / 频道不能创建监控：`POST /monitors` 返回 409，提示先加入。
- SSE 扩展：`monitor.status`、`monitor.match`；现有 `GET /api/v1/events` 格式不变，新增事件类型即可。

#### 3.2.7 CLI

```bash
tg_down monitor ls
tg_down monitor add --peer https://t.me/example --trigger new --video-only \
  --from @alice --exclude-from @spam --pinned any --max-per-hour 20
tg_down monitor test <id> --limit 50
tg_down monitor enable <id>
tg_down monitor disable <id>
tg_down monitor rm <id>
```

> 这些命令只修改数据库，不会自行开始监控；需要 `tg_down serve` 正在运行才会生效。
> `monitor test` 例外：它在 CLI 进程内用一次性短连接做干跑，不依赖 serve。

#### 3.2.8 前端

- 新增 `/monitors` 页：列表（标题、类型、触发、状态、最近命中）、启停开关、编辑/删除。
- 新建向导：粘贴链接或 `@username` → 解析预览 → 选择触发与过滤 → 试运行 → 保存。
- 只允许选择账号已加入的群 / 频道；未加入时提示先加入，不做自动加入、不做轮询。
- 详情抽屉：最近命中、跳过原因、关联下载任务、错误与重连状态。
- 页面只管理配置；实际监控在后端 `serve` 进程内执行。
- RSS 订阅区在详情抽屉内（见 §3.3）。

#### 3.2.9 已确认

1. 不支持未加入的公开频道：不自动加群、不轮询；创建/启用时校验成员身份，非成员返回 409。
2. 默认不启用置顶过滤：`trigger=new` + `pinned=any`；置顶规则作为可选项。
3. 默认只下视频；`filter.media.kinds` 可配置为 photo/video/audio/document。
4. 发送人过滤只接受 ID / `@username`，不支持昵称模糊匹配。
5. 每监控默认 20 条/小时、单文件 2 GB，可配置。
6. 允许同一 peer 被多个账号分别监控，按账号分别限流与计费。

### 3.3 RSS 输出服务（规划）

#### 3.3.1 目标与范围

- 把监控中的群 / 频道消息发布为 RSS 2.0（Media RSS）feed，阅读器可直接订阅。
- 每个监控可以生成一个或多个 feed（全部消息 / 仅媒体 / 仅命中下载）；不做全局聚合 feed。
- 媒体条目默认附带 enclosure：指向本机 `/rss/<token>/media/<message_id>`，直接播放/下载已下载的文件；未下载时不提供。每个 feed 可关闭 enclosure。
- 本期只做 **Telegram → RSS 输出**；外部 RSS → 自动下载不在范围内。
- RSS 不强制下载：监控可关闭 `download_enabled`，只归档并输出 feed。
- 运行位置：由 `serve` 提供，`/rss/*` 不走 Bearer 鉴权，靠 feed token。

#### 3.3.2 复用监控

- 监控已覆盖 peer 解析、成员权限、updates 推送、消息去重与媒体元数据。
- RSS 只需在监控收到消息时多写一份 `peer_messages` 归档，再由只读渲染层输出 XML。
- 监控只覆盖账号已加入的群 / 频道，RSS 同样只对这些会话产出 feed。

#### 3.3.3 数据模型

`peer_messages`（消息归档）：

| 字段 | 说明 |
|---|---|
| `account_id` / `peer_kind` / `peer_id` / `message_id` | 消息定位；`UNIQUE(account_id, peer_kind, peer_id, message_id)` |
| `date` | 消息时间（Unix 秒） |
| `sender_id` / `sender_name` / `sender_username` | 展示；频道帖子可为空 |
| `text` / `entities_json` | 正文与富文本实体 |
| `media_json` | 媒体元数据（kind / file_name / size / mime / duration / grouped_id / thumb 定位） |
| `grouped_id` | 相册分组；非相册为 0 |
| `created_at` | 归档时间 |

- 保留策略：默认 30 天或每 peer 最近 1000 条，后台定期清理；feed 只读最近 N 条。
- 归档发生在监控过滤**之前**：未命中下载规则的消息也会进入，因此 `mode=all/media` 不受下载过滤影响。置顶触发的旧消息会先补进归档。

`rss_feeds`：

| 字段 | 说明 |
|---|---|
| `id` | 自增 |
| `monitor_id` | 关联监控；一个监控可有多条 feed |
| `token_hash` | feed token 的 SHA-256（唯一索引）；明文只在创建/轮换时返回一次 |
| `title` | feed 标题，默认取监控标题 |
| `mode` | `all`（默认）/ `media` / `matched` |
| `enclosure` | 是否输出媒体 enclosure，默认 `true` |
| `limit` | 条目上限，默认 50，范围 1–200 |
| `enabled` | 停用后 token 立即失效 |
| `created_at` / `last_access_at` / `last_build_at` / `updated_at` | |

> 不复用 `monitor_matches`：feed 需要未被下载过滤命中的消息也能出现，而 matches 只记录过滤结果。

#### 3.3.4 Feed 语义

| mode | 条目 |
|---|---|
| `all`（默认） | 归档里的全部消息（文本 + 媒体） |
| `media` | 仅含媒体的消息；相册按消息逐条输出 |
| `matched` | 仅命中监控过滤并创建了下载任务的消息 |

条目字段：

- `title`：`<会话标题> · #<message_id>`；正文首行可作为副标题。
- `link`：`https://t.me/...`；私有 peer 可能只能拼出 `t.me/c/...`，阅读器里点开需要账号权限。
- `guid`：`tg:<peer_id>:<message_id>`，稳定不变。
- `pubDate`：消息时间，RFC 1123Z。
- `description`：发送人 + 正文（XML 转义或 CDATA）+ 媒体摘要。
- `author`：发送人名称（频道帖子可省略）。
- `category`：媒体类型。
- `media:content` / `media:thumbnail`（Media RSS）：媒体直链与封面直链。
- 相册 v1 一条消息一个 item，后续可合并为 `media:group`。

#### 3.3.5 端点与 token

Feed 地址本身就是凭证，阅读器无法带 `Authorization`：

- 高熵随机 token（≥ 32 字节），路径 `/rss/<token>.xml`。
- 数据库只存 `token_hash`；创建/轮换时返回一次明文 URL。
- token 默认永久有效；可停用、轮换，轮换后旧 URL 立即 404。
- 不写日志、不出现在 SSE / 普通 API 响应里。
- 只读，不支持通过 feed 触发下载或修改配置。
- 只输出 RSS 2.0 + Media RSS，不提供 Atom / JSON Feed。

公开端点：

```
GET /rss/{token}.xml?limit=50
    Content-Type: application/rss+xml; charset=utf-8
    ETag: W/"feed-<id>-<latest_message_id>-<config_version>"
    Last-Modified: <max(最新消息时间, feed.updated_at)>
    Cache-Control: private, max-age=60
    支持 If-None-Match / If-Modified-Since → 304

GET /rss/{token}/media/{message_id}
    已下载且状态 done 时返回文件，支持 Range；未下载返回 404

GET /rss/{token}/thumb/{message_id}
    返回封面 JPEG；没有封面 404
```

管理端点（走 Bearer token）：

```
POST   /api/v1/monitors/{id}/feeds   body: {"mode":"all","limit":50,"enclosure":true}
       → 201 {"id":1,"url":"http://host/rss/<token>.xml","token":"<只返回一次>"}
GET    /api/v1/monitors/{id}/feeds   → 200 {"feeds":[...]}   // 不含 token
DELETE /api/v1/feeds/{id}            → 200 {"ok":true}
POST   /api/v1/feeds/{id}/rotate     → 200 {"url":"...","token":"<新 token>"}
```

绝对 URL 默认按 `Host` / `X-Forwarded-Proto` 推导；反向代理场景可增加 `rss_base_url`（规划中）。

#### 3.3.6 渲染与缓存

- 纯函数渲染：`peer_messages` + feed 配置 → XML；统一 XML 转义，避免注入。
- 缓存：按 `(feed_id, latest_message_id, config_version)`；新消息或 feed 配置变化时失效。
- 条件请求：ETag / Last-Modified 命中直接 304，避免阅读器轮询放大。
- 限流：每 token 默认 60 请求/分钟，超限 429；媒体端点单独计算。
- 大文件：media 端点用 `http.ServeContent` 支持 Range。

#### 3.3.7 安全与隐私

- Feed 可能包含私有群 / 频道内容，URL 泄露等同于内容泄露；UI 必须提示「把 URL 当作密码」。
- token 只存哈希，日志脱敏；轮换与停用即时生效。
- 响应加 `X-Robots-Tag: noindex`，不提供目录列表。
- media / thumb 端点校验路径在下载根目录内，只服务该 feed 对应 peer 的已下载任务。
- 默认只监听回环；对外暴露时建议 HTTPS 反向代理。
- 不返回 Telegram 的临时 file reference / 直链。

#### 3.3.8 前端

- 监控详情增加「RSS 订阅」区：创建 feed（mode/limit/enclosure）、复制 URL、停用/轮换、最近访问时间。
- 创建成功后 token 只显示一次，提供「复制」与「我已保存」确认；列表只显示掩码。
- feed 列表提示「URL 即凭证，泄露后请轮换」。
- 不在侧栏单独加入口，RSS 属于监控的附属能力。

#### 3.3.9 已确认

1. 只做 Telegram → RSS 输出；外部 RSS → 下载不在本期。
2. 每个监控一个（可多条）feed；不做全局聚合 feed。
3. 默认条目范围 `all`，保留 `media` / `matched`。
4. 默认提供已下载文件的 enclosure，每个 feed 可关闭；未下载条目不提供。
5. 只输出 RSS 2.0 + Media RSS；本期不做 Atom / JSON Feed。
6. feed token 永久有效，直到手动轮换或停用。

#### 3.3.10 非目标与后续

- 外部 RSS → 自动下载（把 RSS 当输入源）不在本期；若要做，另立「RSS 输入」设计。
- 全文搜索、Web 阅读器、评论、已读状态同步。
- 多 feed 聚合、按关键词生成子 feed、JSON Feed 可作为后续增强。

### 3.4 下载页输入草稿（规划）

#### 3.4.1 目标

- 下载页的三个输入——链接文本、展开正文里的链接、覆盖同名文件——关闭/刷新页面或换浏览器后仍保留。
- 存服务端数据库而不是 `localStorage`：同一实例换设备/浏览器也能恢复，并跟随数据目录一起备份。
- 恢复**输入 + 上一次解析摘要**（只读展示）；不自动重新解析，预览 token 过期后由用户点「重新解析」。

#### 3.4.2 存储

`ui_drafts`：

| 字段 | 说明 |
|---|---|
| `key` | 主键；当前只允许 `downloads`，后续可扩展 |
| `value` | JSON：`{"links":"…","expand_text_links":false,"overwrite":false,"cache":{…}}`；`cache` 可选 |
| `updated_at` | 最后保存时间 |

- 用 JSON 而不是拆列：字段少且可能增加，服务端按结构体解析校验。
- `cache` 与 §3.5.2 同构（summary / chats / items / failures），≤ 32 KiB；只用于展示，不参与下载。
- 单行 upsert；保存空串也保留行（表示用户主动清空，开关状态仍保留）。
- 上限：`links` ≤ 256 KiB、≤ 500 行；下载创建接口仍只接受 200 条链接。
- 不写日志；草稿与下载链接同等敏感。

#### 3.4.3 API

```
GET /api/v1/downloads/draft
→ 200 {
  "links": "",
  "expand_text_links": false,
  "overwrite": false,
  "cache": { … },                  // 可选，上次解析摘要
  "updated_at": 1763500000        // 从未保存时省略
}

PUT /api/v1/downloads/draft
body: {"links":"…","expand_text_links":true,"overwrite":false,"cache":{…}}
→ 200 保存后的完整草稿
→ 400 links 超过 256 KiB / 500 行，或字段类型错误
```

- 全量替换输入；`cache` 省略时保留已有缓存（避免自动保存把摘要冲掉），`cache: null` 显式清除。
- `cache` 非法时丢弃本次缓存、仍保存输入，不返回 400。
- 没有草稿时返回默认值（200），前端不用区分 404。
- 与 `GET /downloads/{id}` 同前缀，静态段 `draft` 必须优先于 `{id}`。

#### 3.4.4 前端

- `use-download-draft` hook：挂载时 GET 一次并恢复输入与摘要；恢复完成前不触发自动保存。
  如果 GET 返回前用户已经开始输入，则放弃这次恢复，保留用户当前输入。
- 自动保存：`links` 变化 debounce 800ms；两个 Switch 立即保存；输入区旁显示「保存中 / 已保存」。
- 关闭页面：`pagehide` 时用 `fetch(..., { keepalive: true })` 尽力补一次；失败静默。
  浏览器对 keepalive 请求体有上限，超大草稿以 debounce 已保存的版本为准。
- 预览成功后顺带更新草稿的 `cache`（与历史记录同一次响应）；失败不影响解析。
- 不自动重新解析；恢复后显示摘要卡（如「3 条消息 · 2 个视频 · 某频道」）与「重新解析」按钮；摘要缺失时只恢复输入。
- 清空按钮：清空 links 和摘要并保存空串，同时清空预览结果。
- 多标签页：last-write-wins，不做冲突合并。
- 保存失败：保留本地输入，显示可重试提示；不阻塞解析与下载。

#### 3.4.5 已确认

1. 草稿全局共享，不按账号区分。
2. debounce 800ms。
3. 恢复输入 + 上次解析摘要（只读），不自动重新解析。
4. 清空后保留空草稿（空 links，保留两个开关状态）。

### 3.5 下载输入历史（规划）

#### 3.5.1 目标

- 在下载页展示最近输入过的链接批次，点击即可回填，避免重新粘贴。
- 与草稿的分工：草稿是「当前正在编辑的输入」，只有一份；历史是「过去解析过的批次」，多份、可删除。
- 只记录 Web 下载页主动解析的输入，不记录监控/RSS/API 创建的任务。

#### 3.5.2 数据模型

`download_inputs`：

| 字段 | 说明 |
|---|---|
| `id` | 自增 |
| `hash` | 唯一键：`links`（换行归一、首尾 trim）+ 两个开关的 SHA-256 |
| `links` | 原始文本，回填用；上限 256 KiB / 500 行 |
| `line_count` | 非空行数 |
| `first_line` | 第一行，截断 160 字符 |
| `cache_json` | 裁剪后的解析摘要（JSON，≤ 32 KiB） |
| `expand_text_links` / `overwrite` | 当时的开关状态 |
| `use_count` | 被记录/重复使用的次数 |
| `created_at` / `last_used_at` | 首次与最近一次 |

缓存结构（白名单，超出截断/丢弃）：

```json
{
  "v": 1,
  "summary": {"total": 3, "ok": 3, "failed": 0, "no_media": 0, "expanded": 0},
  "chats": [{"kind": "channel", "title": "某频道", "username": "example"}],
  "items": [{"message_id": 2522, "chat_title": "某频道", "text": "正文前 200 字",
             "media_kind": "video", "file_name": "clip.mp4", "size": 734003200}],
  "failures": [{"link": "…", "error": "…"}]
}
```

- `items` ≤ 20、`chats` ≤ 5、`failures` ≤ 5；文本/错误各 200 字。
- 不缓存缩略图/预览 URL、entities、`file_id`、`access_hash`。
- 保留最近 20 条；重复内容更新 `last_used_at` / `use_count` / `cache_json`，不新增行。
- 与 `ui_drafts` 分开：草稿会被覆盖，历史要保留多份。

#### 3.5.3 记录时机

- 用户点击「解析预览」并成功返回后，前端把当前输入与**裁剪后的 `cache`** 一起 `POST /downloads/history`；失败静默。
- `cache` 直接来自预览响应，不需要二次请求；服务端按白名单裁剪、截断后落库。
- 只在预览成功后记录；不记录「全部下载」等二次操作。
- 服务端按 `hash` 去重；相同内容再次预览时更新 `last_used_at`、`use_count` 与 `cache_json`。

#### 3.5.4 API

```
GET /api/v1/downloads/history?limit=20
→ 200 {"items": [{
  "id": 3,
  "first_line": "https://t.me/example/2522",
  "line_count": 5,
  "expand_text_links": false,
  "overwrite": false,
  "use_count": 2,
  "summary": {"total": 3, "ok": 3, "failed": 0, "no_media": 0, "expanded": 0},
  "chats": [{"kind": "channel", "title": "某频道", "username": "example"}],
  "cache_truncated": false,
  "created_at": 1763500000,
  "last_used_at": 1763500100
}]}

GET /api/v1/downloads/history/{id}
→ 200 同上 + "links": "完整文本" + "cache": {完整缓存}
→ 404 记录不存在

POST /api/v1/downloads/history
body: {"links":"…","expand_text_links":false,"overwrite":false,"cache":{…}}
→ 201 新建；内容重复时 200 并更新 last_used_at / use_count / cache
→ 400 links 超过 256 KiB / 500 行，或字段类型错误

DELETE /api/v1/downloads/history/{id}   → 200 {"ok":true}
DELETE /api/v1/downloads/history        → 200 {"ok":true,"removed":12}
```

- 列表只返回 `summary` / `chats` / `cache_truncated`，不返回完整 `links` 与缓存条目；回填时取详情。
- `cache_truncated` 表示 `summary.total > len(cache.items)`（只缓存前 20 条）。
- 按 `last_used_at` 倒序；`limit` 默认 20，最大 20；只保留最近 20 条。
- 静态段 `history` 必须优先于 `{id}` 路由。

#### 3.5.5 前端

- 输入区标题旁「历史（N）」按钮；桌面 Popover，窄屏 Sheet。
- 列表行：第一行、`共 N 条`、缓存摘要（`3 条消息 · 2 个视频 · 某频道`）、开关徽章、相对时间、使用次数；点击回填，行尾删除。
- 详情可显示缓存的消息条目与失败原因（只读，最多 20 条），并提供「重新解析」。
- 回填只更新输入框与两个开关（触发草稿自动保存），不自动解析；面板关闭并提示「已填入，点击解析预览」。
- 缓存只用于展示，不直接用缓存数据创建下载。
- 缓存缺失或损坏时退回「共 N 条 + 时间」，不影响回填。
- 「清空历史」二次确认；空状态说明「解析预览后会出现在这里」。
- 不做固定/收藏，只按最近使用排序。

#### 3.5.6 安全与限制

- 历史包含完整链接与部分消息文本，与草稿同等敏感；不写日志，提供清空/单条删除。
- 单条输入上限 256 KiB / 500 行；缓存 ≤ 32 KiB、items ≤ 20、chats ≤ 5、failures ≤ 5，文本/错误各 200 字。
- 只保留最近 20 条；草稿与历史都是全局的，不按账号区分。
- 不记录完整解析结果、完整消息正文或下载任务信息。

#### 3.5.7 已确认

1. 只在「解析预览」成功后记录。
2. 列表默认 20 条，最多保留 20 条。
3. 不做固定/收藏，只按最近使用排序。
4. 历史记录缓存一份裁剪后的解析摘要，用于列表与详情展示；回填仍只恢复输入，缓存不直接用于创建下载。
5. 点击历史只回填，不自动解析。

### 3.6 暂不做（已确认）

以下为 2026-11-22 确认的范围外事项，后续有需求再单独立项：

1. **QR 登录（MTProto）**：暂不做，保留后续；当前用手机号 + 验证码 / 两步密码。
2. **下载后自动转码 / 压缩**：暂不做，保持原文件；避免引入 ffmpeg 依赖。
3. **MTProxy**：暂不支持；现有 SOCKS5 / SOCKS5H / HTTP / HTTPS 足够。
4. **Docker 镜像**：暂不提供；单二进制直接运行。

## 4. 路线图

### 4.1 里程碑总表

| 阶段 | 内容 | 预估 | 状态 |
|---|---|---|---|
| M0 | 脚手架、路由与中间件、SQLite 建表、Vite 代理、`go:embed` | 0.5–1 天 | ✅ |
| M1 | MTProto 登录、session 持久化（Bot API 未做） | 2 天 | ✅（部分） |
| M2a | 会话/消息读取接口：dialogs、folders、chat info、messages、pinned、mark-read、`downloads/from-messages` | 3 天 | 🚧 |
| M2b | 会话列表 + 消息流 UI（仿 Telegram Desktop、文件夹/归档、收藏夹、私聊、标记已读） | 5–6 天 | 🚧 |
| M2c | 媒体/相册、多选批量下载、信息面板、移动端与可访问性 | 3 天 | 🚧 |
| M3 | 单文件下载 + `.part` 续传 + SSE | 2 天 | ✅ |
| M4 | 队列、并发限流、FloodWait 恢复、失败重试 | 1–2 天 | ✅（FloodWait 未做） |
| M5 | 任务页 / 设置页 / 批量下载 / 错误提示 | 2 天 | ✅ |
| M6 | 单二进制、Windows 路径与权限、README | 1 天 | ✅ |
| M7 | 监控核心：模型、CRUD、peer 解析、`serve` 生命周期、连接池、直接入队、去重 | 3 天 | 🚧 |
| M8 | 监控过滤与补扫：sender/pinned/media/text/forward、pin 触发、backfill、干跑 | 2–3 天 | 🚧 |
| M9 | 监控稳定性与 UI：gap 恢复、限流退避、`/monitors` 页与 SSE | 2–3 天 | 🚧 |
| M10 | RSS 基础：`peer_messages`、`rss_feeds`、RSS 2.0、ETag/304 | 2 天 | 🚧 |
| M11 | RSS 媒体与打磨：media/thumb、Media RSS、enclosure 开关、前端复制/轮换 | 1–2 天 | 🚧 |
| M12 | 下载草稿：`ui_drafts`、GET/PUT、摘要缓存、自动保存 | 1 天 | 🚧 |
| M13 | 下载历史：`download_inputs`、去重/清理、历史面板 | 1–1.5 天 | 🚧 |

- 含会话界面的 MVP（M0–M6）约 **15–18 个工作日**。
- 监控（M7–M9）约 **7–9 天**；RSS（M10–M11）约 **3–4 天**；草稿 + 历史约 **2–2.5 天**。

### 4.2 依赖与顺序

- M2a 依赖现有下载与链接解析；M2b/M2c 依赖 M2a。
- M7 的连接池改造会顺带改善现有下载/预览的 session 并发问题；M8/M9 依赖 M7。
- RSS 依赖 M7 的消息归档；建议 M9 的 supervisor 稳定后再对外暴露 feed。
- 草稿与历史相互独立，可与 M2/M7 并行。
- M0–M2 可基于 `omni_toolbox` 骨架改造，复用清单见 [reference.md](./reference.md)，预计省 2–3 天。

## 5. 风险与对策

### 5.1 通用

| 风险 | 影响 | 对策 |
|---|---|---|
| MTProto 账号被封 | 工具不可用 | 用小号；并发 ≤ 3；不频繁拉历史；不在登录态做批量操作 |
| 单文件 2GB/4GB 上限 | 大文件无法下载 | 入队前校验 size，UI 明确提示 |
| `FLOOD_WAIT` | 队列整体阻塞 | 设计为可挂起可恢复；**当前未实现**，限流任务会失败，需手动重试 |
| Bot API 20MB 限制 | 大文件下载失败 | 文档说明；或引导自建 local bot-api-server |
| 本地 bot-api-server 部署复杂 | 用户放弃 | 作为可选项，`botapi.base_url` 覆盖 |
| CGO / Windows 交叉编译 | 打包麻烦 | 使用纯 Go SQLite 驱动 |
| session/token 泄露 | 账号被盗 | 文件权限收紧、接口不回传、默认只监听回环 |
| 文件名与路径注入 | 写到根目录外 | 统一 sanitize + `filepath.IsLocal` 校验 |

### 5.2 会话界面

| 风险 | 影响 | 对策 |
|---|---|---|
| 会话内容在 Web 暴露 | 私聊/私有群内容被看到 | 默认回环 + Bearer token；会话页只读、不落库 |
| 翻历史触发 `FLOOD_WAIT` | 消息加载失败 | 分页 + 并发限制 + 可读错误提示；不在打开会话时预取过多 |
| 标记已读影响其他客户端 | 手机端/Desktop 未读被清 | 只在显式点「标记已读」时调用，并提示会同步 |
| 消息/会话量大 | 渲染卡顿、滚动掉帧 | 虚拟滚动 + 图片懒加载 + 行级 `memo` |

### 5.3 监控与 RSS

| 风险 | 影响 | 对策 |
|---|---|---|
| 同 session 多客户端 | session 文件写入冲突、msg_id/seq 异常 | M7 引入每账号单客户端连接池；监控与下载共用 |
| 账号被限流/封禁 | 监控中断 | 低频、每监控令牌桶、`FLOOD_WAIT` 退避、不在登录态批量拉历史 |
| 频道未加入 | 无法监控 | 创建/启用时校验成员身份，非成员返回 409；不自动加群、不轮询 |
| 置顶语义误用 | 反复下载旧置顶帖 | `pinned` 三态 + 去重；默认 `any`，文档写清组合 |
| updates 断档 | 漏消息 | `updates.Manager` 持久化状态 + `OnTooLong` / `OnChannelGap` 回退历史补扫 |
| 磁盘增长 | 自动下载占满磁盘 | 每监控速率上限、最小/最大文件大小、保留策略（后续） |
| feed token 泄露 | 私有内容被订阅 | token 只存哈希、可轮换/停用、日志脱敏、UI 提示「URL 即凭证」 |
