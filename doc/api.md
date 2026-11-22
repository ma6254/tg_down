# tg_down API 文档

所有接口前缀 `/api/v1`（RSS 输出例外，见 §2.4），JSON 收发，SSE 用于进度推送。

> **状态说明**：§1 为已实现接口，与代码一致；§2 为规划中接口，尚未实现（请求返回 404）。
> 调用方以 §1 为准。`/api/openapi.json` 与 `/swagger` 只覆盖 §1。

## 0. 导读

### 0.1 状态与索引

| 分组 | 接口 | 状态 |
|---|---|---|
| 通用 | `GET /health`、`GET /system` | ✅ |
| 账号 | 探测、列表、切换、登出、删除、详情、在线资料、头像 | ✅ |
| 链接 | `POST /links/preview`、`GET /media/thumbnail/{token}` | ✅ |
| 下载 | 创建、列表/详情、暂停/继续/重试、删除、任务缩略图 | ✅ |
| 事件 | `GET /events`（SSE） | ✅ |
| 设置 | `GET/PUT /settings` | ✅ |
| 账号登录扩展 | Bot Token、MTProto 三步登录 | 🚧 §2.1 |
| 会话 | 列表、文件夹、信息、已读、消息、置顶、按消息下载 | 🚧 §2.2 |
| 监控 | CRUD、干跑、命中记录、peer 解析、`monitor.*` SSE | 🚧 §2.3 |
| RSS | feed 管理、`/rss/*` 公开端点 | 🚧 §2.4 |
| 下载页 | 输入草稿、输入历史 | 🚧 §2.5 |

### 0.2 通用约定

**错误格式**

```json
{
  "error": {
    "code": "not_found",
    "message": "账号不存在: 账号 ID 9 不存在"
  }
}
```

| HTTP | code | 含义 |
|---|---|---|
| 400 | `bad_request` | 参数缺失/类型错 |
| 401 | `unauthorized` | token 缺失或错误 |
| 404 | `not_found` | 账号/任务/消息不存在，或接口尚未实现 |
| 409 | `conflict` | 任务状态不允许该操作；没有当前账号 / session 不可用 |
| 500 | `internal_error` | 其他服务端错误 |
| 501 | `not_implemented` | 服务端未接入该能力（正常构建不会出现） |
| 502 | `preview_failed` / `download_failed` / `live_failed` / `thumbnail_failed` | 连接 Telegram 失败或 Telegram 报错 |
| 504 | `probe_timeout` / `live_timeout` / `logout_timeout` / `preview_timeout` / `download_timeout` / `thumbnail_timeout` | 联网操作超时 |

> 监听非回环地址却未配置 token 时，进程在启动阶段直接退出，不会返回 403。
> `FLOOD_WAIT` 目前只把可读原因写进任务或解析结果的 `error` 字段，没有独立的 422 与
> `retry_after`；自动挂起恢复尚未实现。

**鉴权**

- 默认只监听 `127.0.0.1`，此时不需要 token。
- 对外暴露（非回环地址）时，除静态资源、`/swagger/*` 与 `/api/openapi.json` 外，
  所有 `/api/*`（**包括 health**）都要求 `Authorization: Bearer <token>`。
- 单用户模型，没有用户注册与登录会话；token 即身份。
- `/rss/*`（规划中）不走 Bearer 中间件，靠 feed token 鉴权。

**客户端初始化**

没有匿名的 `/session` 探测接口；前端直接用 health 判断：

- 200：后端可用（免鉴权，或已有 token 有效）
- 401：需要 token，展示登录页
- 网络错误 / 5xx：展示离线页并允许重试

## 1. 已实现

### 1.1 健康检查

```
GET /api/v1/health
→ 200 {"status":"ok","version":"dev","uptime_seconds":123}
```

启用 token 后该接口同样要求 Bearer token；`uptime_seconds` 便于确认进程是否刚重启。

### 1.2 系统信息

```
GET /api/v1/system
→ 200 {
  "version": "dev",
  "commit": "none",
  "build_time": "unknown",
  "go_version": "go1.27.1",
  "os": "windows",
  "arch": "amd64",
  "uptime_seconds": 123
}
```

`version`、`commit` 与 `build_time` 由构建脚本通过 `-ldflags` 注入（直接 `go build` 时分别是
`dev` / `none` / `unknown`），用于确认线上跑的是哪个构建、是否与预期提交一致；
`go_version`、`os`、`arch` 是编译期运行环境，`uptime_seconds` 是进程已运行秒数。
启用 token 后该接口同样要求 Bearer token。

### 1.3 账号

同一套接口覆盖 MTProto 用户账号与 Bot API 机器人账号，用 `kind` 区分。

#### 1.3.1 探测本机 Telegram Desktop

等价于 `tg_down account login --probe`：读取本机 Telegram Desktop 的 tdata，
用其中已有的授权逐个连接 Telegram 校验，默认把有效账号导入
`<数据目录>/sessions/`（默认 `<exe 目录>/sessions/`）与数据库。

```
POST /api/v1/accounts/probe
body: {"tdata":"C:\\Users\\me\\AppData\\Roaming\\Telegram Desktop\\tdata",  // 留空自动查找
       "tdata_passcode":"",       // Telegram Desktop 本地密码
       "timeout_seconds":30,      // 5-300，默认 30
       "import":true}             // false 时只探测、不落盘
→ 200 {
  "tdata_path": "C:\\...\\tdata",
  "accounts": [{
    "index": 0,
    "user_id": 355927778,
    "name": "...",
    "username": "ma6254",
    "phone": "+86138...",
    "devices": [{"current": true, "app_name": "Telegram Desktop", "platform": "Windows", "active_at": 1795058692}],
    "error": ""                  // 单个账号失败时写原因，不影响其他账号
  }],
  "imported": 1,                 // 新写入的 session 数
  "skipped": 0,                  // session 文件已存在被保留的数量
  "selected_account_id": 7       // 导入后库里只剩一个账号时自动选中
}
→ 400 {"error":{"code":"probe_failed","message":"未找到 Telegram Desktop 的 tdata 目录…"}}
→ 504 {"error":{"code":"probe_timeout","message":"探测超时（30s）…"}}
```

#### 1.3.2 账号列表

```
GET /api/v1/accounts
→ 200 {
  "accounts": [{
    "id": 1,                    // 本地自增 ID，不是 Telegram user.id
    "kind": "mtproto",          // mtproto | botapi
    "source": "desktop",        // manual（手动登录）| desktop（桌面导入）| bot；旧数据省略
    "name": "+86****1234",      // bot 则为 username
    "username": "ma6254",
    "phone": "8613800000000",   // 不带 + 号，展示时打码
    "state": "authed",          // unauthed | authed | error
    "last_error": "",           // 无错误时省略
    "telegram_id": 355927778,   // Telegram user.id；未绑定/未联网时省略
    "created_at": 1763500000,   // Unix 秒
    "current": true             // 是否为当前选中账号
  }],
  "current_account_id": 1       // 未选中时省略
}
```

说明：

- `telegram_id` 是 Telegram 的 `user.id`，换手机号、改用户名都不会变；本地 `id` 只是数据库主键。
- `source` 记录账号来源：`manual` 手动登录、`desktop` 从 Telegram Desktop 导入、`bot` Bot 接入；旧数据为空，前端显示“未记录”。
- 登录/导入时会写入 `telegram_id`；旧账号首次调用 `--live` 或 `GET /accounts/{id}/live` 时自动补写。
- 去重顺序：优先 `telegram_id`，没有时退回 session 文件路径。

#### 1.3.3 切换当前账号

```
POST /api/v1/accounts/{id}/select
→ 200 {"ok":true,"current_account_id":3}
→ 404 {"error":{"code":"not_found","message":"账号不存在: 账号 ID 9 不存在…"}}
→ 409 {"error":{"code":"conflict","message":"账号的 session 文件不可用…"}}
```

切换前会确认账号存在、session 文件可读；结果写入 `settings.current_account_id`，
`GET /api/v1/accounts` 的 `current` / `current_account_id` 随之变化。

#### 1.3.4 退出登录

```
POST /api/v1/accounts/{id}/logout
→ 200 {"ok":true,"revoked":true,"removed_session":false}
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}
→ 409 {"error":{"code":"conflict","message":"桌面导入的账号不能注销…"}}
→ 504 {"error":{"code":"logout_timeout","message":"登出超时，请稍后重试"}}
```

- 先尽力向 Telegram 发送 `auth.logOut` 吊销 session；`revoked=true` 表示吊销成功。
- 网络失败不阻断本地清理，此时返回 `revoked=false`，前端会提示“本地已删除，Telegram 端注销失败”。
- 随后删除数据库记录与 session 文件；删除当前账号时会清空当前选择。
- 已下载的文件不受影响。
- 桌面导入（tdata）的账号与 Telegram Desktop 共用 auth key，注销会影响桌面端，因此返回 409，只能改用下面的删除接口。

#### 1.3.5 删除账号（仅本地）

```
DELETE /api/v1/accounts/{id}
→ 200 {"ok":true,"removed_session":true}
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}
```

- 只删除本地数据库记录、session 文件与头像缓存，**不会**向 Telegram 注销会话。
- 桌面导入的账号与 Telegram Desktop 共用登录，用这个接口只移除本地副本，桌面端不受影响。
- 需要同时注销 Telegram 会话时用上面的 `logout`。

#### 1.3.6 查看账号详情

```
GET /api/v1/accounts/{id}
→ 200 {
  "id": 1,
  "kind": "mtproto",
  "source": "manual",            // manual | desktop | bot；旧数据省略
  "name": "...",
  "username": "ma6254",
  "phone": "8613800000000",
  "state": "authed",
  "telegram_id": 355927778,      // Telegram user.id；未绑定或未联网时省略
  "created_at": 1763500000,
  "updated_at": 1763500120,      // 从未更新时省略
  "current": true,
  "session_present": true        // session 文件是否还在，false 说明需要重新登录
}
→ 400 账号 ID 不是正整数
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}
```

删除账号见 §1.3.5；两者共用 `DELETE /api/v1/accounts/{id}`。

#### 1.3.7 在线资料（--live）

等价于 `tg_down account info <id> --live`：临时连一次 Telegram，读取最新资料
（名称、会员、生日、简介）与已登录客户端。只查询、不回写资料字段；首次联网拿到
`user.id` 时会补写账号的 `telegram_id` 绑定。单次最长 30 秒。

```
GET /api/v1/accounts/{id}/live
→ 200 {
  "user_id": 355927778,          // Telegram 用户 ID，与本地账号记录的 id 无关
  "name": "张三",
  "username": "ma6254",
  "phone": "8613800000000",      // 不带 + 号，展示时打码
  "premium": true,               // 会员；false 时省略
  "bot": false,                  // Bot 账号；false 时省略
  "about": "……",                 // 简介；为空时省略
  "birthday": {"day": 2, "month": 3, "year": 2000},  // year 省略表示未公开年份
  "devices": [{                  // 与探测接口的 devices 同构
    "current": true, "app_name": "Telegram Desktop", "platform": "Windows",
    "active_at": 1795058692
  }],
  "fetched_at": 1795058692       // 查询时间，Unix 秒
}
→ 404 账号不存在
→ 409 账号没有可用的 session 文件（Bot 账号也走这一条）
→ 502 联网失败：session 已失效、网络不可达或 Telegram 报错
→ 504 查询超过 30 秒
```

#### 1.3.8 账号头像

```
GET /api/v1/accounts/{id}/avatar
→ 200 image/jpeg
```

- 头像字节缓存在 `<数据目录>/avatar/<user_id>.jpg`（默认 `<exe 目录>/avatar/`，首次访问时创建），
  文件名是 Telegram 的 user.id，换手机号/用户名不会变；旧版本按本地账号 ID
  命名的文件会在首次访问时自动迁移到 user_id 下。
- 群聊、频道与其他用户的头像也缓存在同一目录，命名为
  `peer-<kind>-<id>-<photoID>.jpg`（预览页头像与账号头像共用这个目录）。
- 有效期 24 小时；到期后联网刷新，失败时继续用旧文件兜底。
- 账号没有头像（或被隐藏）时返回 404，前端回退到名称首字符。
- 没有 session 且无缓存时返回 409；联网失败返回 502、超时 504。
- 浏览器侧缓存 1 小时。

### 1.4 链接解析

等价于 CLI 的 `tg_down download -u <链接>`：解析链接指向的消息，返回正文、
正文里的文字链接与媒体元数据。只联网读取，不写数据库、不创建下载任务。

#### 1.4.1 预览

```
POST /api/v1/links/preview
body: {
  "links": ["https://t.me/JPD233/2522"],
  "expand_text_links": false     // 可选；true 时展开正文里的文字链接，只展开一层
}
→ 200 {
  "items": [{
    "link": "https://t.me/JPD233/2522",
    "message_id": 2522,
    "chat": {
      "kind": "channel",            // user | group | channel
      "id": 3713824040,
      "title": "…",
      "username": "JPD233",
      "broadcast": true,            // kind=channel 时：true 为频道，false 为超级群
      "self": false,                // kind=user 时：true 表示当前账号（收藏夹）
      "avatar_url": "/api/v1/media/thumbnail/…"  // 群聊/频道头像；私聊通常省略
    },
    "sender": {                     // 仅群聊消息有；频道/私聊省略
      "id": 355927778,
      "name": "Jake Smith",
      "username": "jake",
      "avatar_url": "/api/v1/media/thumbnail/…"  // 发送者头像；没有时省略
    },
    "text": "…",
    "links": [{"text": "第1集", "url": "https://t.me/JPD233/2522?comment=12454"}],
    "media": {
      "kind": "video",              // photo | video | audio | document
      "file_name": "…mp4",
      "size": 324564430,
      "message_id": 2522,           // 这条媒体所在消息
      "link": "https://t.me/JPD233/2522",  // 可单独下载的链接；拼不出时省略
      "mime": "video/mp4",
      "duration": 820,              // 秒，非音视频省略
      "width": 1280, "height": 720,
      "grouped_id": 0,              // 相册分组，非相册省略
      "downloadable": true,         // 是否超过当前账号的单文件上限
      "max_size": 2147483648,       // 普通 2 GB / Premium 4 GB
      "thumbnail_url": "/api/v1/media/thumbnail/<token>"  // 列表封面，没有缩略图时省略
    },
    "medias": [{                    // 相册：全部媒体（按消息 ID 升序）；非相册省略
      {"kind": "photo", "file_name": "photo_1.jpg", "size": 78935,
       "message_id": 8074, "link": "https://t.me/AV_mys/8074", "grouped_id": 123, "downloadable": true},
      {"kind": "photo", "file_name": "photo_2.jpg", "size": 58988,
       "message_id": 8075, "link": "https://t.me/AV_mys/8075", "grouped_id": 123, "downloadable": true},
      {"kind": "video", "file_name": "clip.mp4", "size": 2611654942,
       "message_id": 8077, "link": "https://t.me/AV_mys/8077", "grouped_id": 123, "downloadable": false}
    ]
  }],
  "summary": {"total": 1, "ok": 1, "failed": 0, "expanded": 0, "no_media": 0}
}
```

会话类型由 `chat.kind` + `broadcast` + `self` 推导：广播频道显示“频道”，
超级群/普通群显示“群聊”，其他用户显示“私聊”，当前账号自己的会话显示“收藏夹”。
群聊还会返回群头像 `chat.avatar_url` 与发送者 `sender`（名称/头像）；
匿名管理员等没有个人发送者的消息可能省略 `sender`。频道也会返回 `chat.avatar_url`。
所有头像都是 30 分钟有效的 token 地址。

补充：

- 单条链接失败只写在该条的 `error` 字段，整体仍返回 200；`links` 为空或超过 200 条返回 400。
- 纯文本消息没有 `media`，正文与链接照常返回。
- 相册消息会额外返回 `medias`：每条媒体带自己的 `message_id` 与 `link`，前端按列表逐条展示和入队；`media` 仍保留为链接指向的那一条。`POST /downloads` 传相册链接时，服务端会为每条媒体各建一个任务。
- `expand_text_links=true` 时，展开出的条目带 `expanded: true`，并计入 `summary.expanded`；展开上限 200 条（超过时 `summary.truncated=true`），正文里不是消息链接的条数计入 `summary.skipped_links`。
- 错误：400 `bad_request`；409 `conflict`（没有当前账号 / session 不可用）；502 `preview_failed`（连接或 Telegram 报错）；504 `preview_timeout`。

#### 1.4.2 媒体缩略图

```
GET /api/v1/media/thumbnail/{token}
→ 200 image/jpeg
```

`token` 来自解析结果的 `media.thumbnail_url`，有效期 30 分钟；服务端按需下载并缓存，
前端用 `loading="lazy"` 只加载可见的封面。没有缩略图的媒体（如音频、PDF）不会返回该字段。
媒体缩略图字节写盘到 `<数据目录>/thumbnail/`（按媒体身份分片）；
群聊/频道/发送者头像写盘到 `<数据目录>/avatar/peer-<kind>-<id>-<photoID>.jpg`。
token 过期后重新预览仍可直接命中；token 失效返回 404，下载失败返回 502；前端应退回类型图标。

### 1.5 下载任务

任务由后台 worker 执行，状态落库；服务重启时残留的 `running` 任务会转为 `paused`，
`pending` 任务自动重新入队。进度通过 SSE 推送，未连接时回退到列表轮询。

#### 1.5.1 创建

```
POST /api/v1/downloads
body: {
  "links":["https://t.me/JPD233/2522","https://t.me/JPD233/2522?comment=12454"],
  "overwrite": false,           // 可选；true 时覆盖同名文件并从零下载
  "single": false               // 可选；true 时只下载链接指向的那条消息，不展开相册
}
→ 201 {
  "created":  [42],                       // 新建并入队的任务
  "existing": [41],                       // 之前已存在（同一链接），不重复下载
  "skipped":  [{"link":"…","reason":"该消息没有可下载的媒体"}],
  "failed":   [{"link":"…","error":"链接无法解析"}]
}
```

- 链接唯一：同一个链接重复创建返回 `existing`，不会重复下载。
- `single=true`：只下载链接直接指向的那条消息，不把相册展开成整组；
  默认 `false` 与 CLI 一致，相册链接会展开成整组任务。网页媒体列表里的
  单行「下载」按钮传 `true`，「全部下载」传 `false`。
- `overwrite=true`：目标位置已有同名文件时直接替换（不再生成「名称 (1).ext」），
  并丢弃已有 `.part` 从零下载；默认 `false` 时同名文件加序号，同名同大小视为已完成。
  该标记随任务落库，任务详情里的 `overwrite` 字段可用于核对。
- 没有媒体或超过账号单文件上限的链接进 `skipped`；解析/查询失败的进 `failed`，
  整体仍返回 201。
- 一次最多 200 条链接。
- 私聊 / 收藏夹等没有 `t.me` 链接的消息，用 `POST /downloads/from-messages`（规划中，见 §2.2）。
- 错误：400 `bad_request`；409 `conflict`（没有当前账号 / session 不可用）；
  502 `download_failed`（连接或 Telegram 报错）；504 `download_timeout`。

#### 1.5.2 列表与详情

```
GET /api/v1/downloads?status=running&limit=100
→ 200 [{
  "id": 42,
  "link": "https://t.me/…",
  "account_id": 1,
  "chat_id": 3713824040,
  "chat_title": "某频道",
  "message_id": 2522,
  "file_name": "clip.mp4",
  "file_size": 734003200,
  "kind": "video",
  "mime": "video/mp4",
  "status": "running",          // pending|running|paused|done|failed
  "offset": 104857600,          // 已下载字节数
  "speed": 2457600,             // B/s，仅 running
  "error": "",
  "save_path": "downloads/某频道/clip.mp4",
  "overwrite": false,
  "thumbnail_url": "/api/v1/media/thumbnail/<token>",  // 没有封面时省略
  "preview_url": "/api/v1/media/thumbnail/<token>",    // 大图预览，没有时省略
  "created_at": 1763500000,
  "updated_at": 1763500120
}]

GET /api/v1/downloads/{id}      → 200 同上；400 ID 非法；404 不存在
```

- `status` 只接受五个状态值；`limit` 范围 1-200，默认 100。
- `running` 的 `offset`/`speed` 来自内存实时进度，其余状态取数据库。

#### 1.5.3 控制

```
POST   /api/v1/downloads/{id}/pause    → 200 暂停后的任务；取消传输并保留 .part
POST   /api/v1/downloads/{id}/resume   → 200 仅 paused 可用，从断点继续
POST   /api/v1/downloads/{id}/retry    → 200 仅 failed 可用，从断点继续
DELETE /api/v1/downloads/{id}?delete_file=true
                                       → 200 {"ok":true}
```

- `pause` 对 `pending` 任务同样生效，直接出队。
- 删除运行中任务会先取消 context；`.part` 总是清理，最终文件只在 `delete_file=true` 时删除。
- 状态不允许时返回 409 `conflict`。

#### 1.5.4 任务缩略图

```
GET /api/v1/downloads/{id}/thumbnail
→ 200 image/jpeg
```

- 任务列表里的 `thumbnail_url` 是创建时登记的封面地址（内存 token，约 30 分钟有效），
  `preview_url` 是更大尺寸的预览图，图片查看器优先用它；该地址失效或历史任务没有地址时，
  前端改用本接口。
- 封面字节缓存在 `<数据目录>/thumbnail/<两位分片>/<sha256>.jpg`（`TG_DOWN_HOME` 下，
  默认 `<exe 目录>/thumbnail`）。缓存键由 `媒体类型 + 文件 ID + 尺寸类型` 决定，
  与 token、进程无关：预览页和任务卡片共享同一份文件，重启后仍能命中。
- 本接口首次访问会重新解析该任务的消息、下载封面并写盘，同时把缓存键记到任务上；
  之后同一任务直接读文件，不再联网。没有可用封面时返回 404。
- 一次首屏可能同时加载多行，服务端最多并发抓取 3 张，避免打开过多 Telegram 连接。
- 旧版本写在 `download_tasks.thumbnail` 列里的 BLOB 已不再使用；服务启动时的迁移会自动
  `DROP COLUMN` 删除该列（数据只删这一列，任务行保留）。SQLite 不会自动缩小文件，
  需要回收磁盘空间可手动执行一次 `VACUUM`。

#### 1.5.5 尚未实现

- 下载完成后的大小校验与 CDN 分块哈希校验。
- `FLOOD_WAIT` 自动挂起与定时恢复（当前只把限流原因写进任务 `error`）。

### 1.6 实时进度（SSE）

```
GET /api/v1/events
Accept: text/event-stream
Authorization: Bearer <token>     # 启用 token 时必需
```

前端用 `fetch` 读流（`EventSource` 无法带 Authorization 头）。事件类型：

```
event: task.update
data: {完整 DownloadTask}          // 新建、开始、暂停、完成、失败时推送

event: task.progress
data: {"id":42,"offset":125829120,"speed":2457600,"size":734003200}

event: task.deleted
data: {"id":42}

event: ping
data: {}
```

- `task.update` 的 data 与 `GET /downloads/{id}` 同构，前端直接整条替换。
- `task.progress` 每约 1.5 秒一条，只更新 offset/speed；暂停或结束后不再推送。
- 服务端每 15 秒发一次 `ping`；连接建立时也会先发一条。
- 慢客户端会丢中间事件（有界缓冲），不会拖住下载任务。
- 前端断线后指数退避重连，重连成功时重新拉一次 `GET /downloads` 对齐；
  未连接期间退回 2 秒轮询。
- 事件只覆盖下载任务；`account.state` 等账号事件尚未推送。

### 1.7 设置

配置是「键 → 字符串」的扁平表，接口返回数组（不是对象）：

```
GET /api/v1/settings
→ 200 {
  "settings": [
    {"key":"current_account_id","type":"integer","value":"1","description":"当前选中的账号 ID（由 account switch 维护）","known":true,"read_only":true},
    {"key":"http_proxy","type":"string","value":"http://user:***@127.0.0.1:7897","description":"出站代理…","known":true},
    {"key":"http_proxy_enabled","type":"boolean","default":"true","value":"true","description":"是否启用 http_proxy…","known":true}
  ]
}

PUT /api/v1/settings
body: {"http_proxy":"socks5://127.0.0.1:1080"}   // 值为 null 表示删除
→ 200 {完整设置}
```

- `type` 是值的逻辑类型（`string` / `integer` / `boolean`），由后端 `config.items` 定义；
  `value` 仍以字符串存储，前端可据此选择输入控件。
- `default` 是未配置时使用的默认值；目前 `http_proxy_enabled` 默认 `"true"`，`api_custom_enabled` 默认 `"false"`。
- 服务启动时如果 `settings` 表为空，会写入带非空默认值的配置项
  （当前是 `http_proxy_enabled=true` 与 `api_custom_enabled=false`）；空默认值表示未配置，不会落库。
- 只允许写 `config.items` 里登记的键；`current_account_id` 只读，由账号接口维护。
- **代理密码脱敏**：`http_proxy` 的密码在响应里固定返回 `***`，不会回传明文。
  提交时如果密码仍是 `***`，服务端沿用已保存的密码（Web 可以只改主机/端口而不重输密码）；
  要更换密码请提交完整 URL。
- **代理开关**：`http_proxy_enabled` 是布尔值，未配置时视为 `true`；
  设为 `false` 后即使 `http_proxy` 有值也直连，代理地址本身保留。清除该键会恢复默认（启用）。
- 清除代理：`{"http_proxy": null}`。
- 未知键、非法值、只读键返回 400 `bad_request`。

## 2. 规划中

> 以下接口均未实现，请求返回 404。设计细节见 [plan.md](./plan.md) 对应章节。

### 2.1 账号登录扩展

#### 2.1.1 Bot 账号接入

> 当前版本没有 Bot API 客户端：`POST /accounts/bot` 会返回 404。

```
POST /api/v1/accounts/bot
body: {"name":"my_bot","token":"123456:ABC..."}
→ 201 {"id":2,"kind":"botapi","name":"my_bot","state":"authed"}
```

服务端会调用 `getMe` 校验；token 非法返回 400 `bad_request`，不落库。

#### 2.1.2 MTProto 登录（三步）

> 当前版本只能通过 `account login`（CLI）或 `POST /accounts/probe` 接入 MTProto 账号；
> 以下三个 HTTP 接口尚未实现，会返回 404。

```
POST /api/v1/accounts/mtproto/send-code
body: {"phone":"+8613800000000"}
→ 200 {"phone_code_hash":"abc...","account_id":3}
```

```
POST /api/v1/accounts/mtproto/sign-in
body: {"account_id":3,"phone":"+8613800000000","code":"12345","phone_code_hash":"abc..."}
→ 200 {"state":"authed"}
→ 202 {"state":"password_required"}      // 需要两步密码
→ 400 {"error":{"code":"bad_request","message":"验证码错误"}}
```

```
POST /api/v1/accounts/mtproto/password
body: {"account_id":3,"password":"******"}
→ 200 {"state":"authed"}
→ 400 密码错误
```

> 登录成功后 session 文件写入数据目录，数据库只记录相对路径，重启免登录。
> 后续可加 `POST /api/v1/accounts/mtproto/qr` 与 `GET .../qr/poll`（暂不做）。

### 2.2 会话与消息

> 当前版本没有这些接口；Web 端也没有会话/消息页。下面是 M2 的接口设计。
> `{peer}` 统一用 `kind:id` 编码（`channel:3713824040`、`group:123456`、`user:355927778`），
> 避免不同 peer 类型的 ID 歧义；收藏夹是当前账号的 `user:<self_id>`。

#### 2.2.1 会话列表

```
GET /api/v1/accounts/{id}/chats?folder_id=0&limit=100&offset=0
→ 200 {
  "chats": [{
    "peer": "channel:3713824040",
    "kind": "channel",              // channel | group | user
    "title": "某频道",
    "username": "somechannel",
    "avatar_url": "/api/v1/media/thumbnail/…",  // 没有时省略
    "self": false,                  // true = 收藏夹
    "pinned": true,                 // 会话置顶
    "muted": false,
    "unread_count": 3,              // 只展示，不因查看而清零
    "last_message": {
      "message_id": 2522,
      "date": 1763500000,
      "text": "…",
      "sender_name": "…",           // 群组/私聊
      "media_kind": "video"         // 没有媒体时省略
    }
  }],
  "next_offset": 100                // 没有更多时省略
}
```

- `folder_id`：`0` 全部（默认）、`1` 归档、`>=2` 自定义文件夹；由 `/chats/folders` 返回。
- 排序：置顶优先，其余按 `last_message.date` 倒序。
- `unread_count` 只展示，**不会因打开会话自动清零**；需要清零时调用 `POST .../read`。

#### 2.2.2 文件夹与归档

```
GET /api/v1/accounts/{id}/chats/folders
→ 200 {"folders": [{
  "id": 0,
  "title": "全部",
  "kind": "all"                     // all | archive | custom
}, {
  "id": 1,
  "title": "归档",
  "kind": "archive",
  "count": 8                        // 可选
}, {
  "id": 7,
  "title": "资源",
  "emoticon": "📦",                 // 可选
  "kind": "custom",
  "count": 12
}]}
```

- 来自 Telegram `messages.getDialogFilters`；归档是固定项（`id=1`），自定义文件夹从 2 开始。
- `count` 可选，仅用于页签角标；以会话列表实际结果为准。
- 取不到 filters 时退回只有「全部」一个页签，不阻塞会话列表。

#### 2.2.3 会话信息

```
GET /api/v1/accounts/{id}/chats/{peer}
→ 200 {
  "peer": "channel:3713824040",
  "kind": "channel",
  "title": "某频道",
  "username": "somechannel",
  "about": "…",
  "members_count": 12345,          // 群/频道
  "pinned_message_id": 2522,       // 没有置顶时省略
  "avatar_url": "…",
  "self": false
}
```

#### 2.2.4 标记已读

```
POST /api/v1/accounts/{id}/chats/{peer}/read
body: {"max_id": 0}        // 可选；0/省略表示读到最新
→ 200 {"ok":true,"unread_count":0}
→ 400 参数非法
→ 404 会话不存在
→ 409 没有当前账号 / session 不可用
→ 502 read_failed / 504 read_timeout
```

- 只有显式调用才改变已读状态；**打开会话不会自动调用**。
- 会同步到该账号的其他客户端，按钮旁需要有相应提示。
- `max_id > 0` 时只标记到该消息，为后续「标记到此为止」预留。
- 服务端按 peer 类型调用 `messages.readHistory`（普通群/私聊）或 `channels.readHistory`（频道/超级群）。

#### 2.2.5 消息列表

```
GET /api/v1/accounts/{id}/chats/{peer}/messages?offset_id=0&limit=50&media_only=false
→ 200 {
  "items": [{
    "message_id": 8888,
    "date": 1763500000,
    "peer": "channel:3713824040",
    "out": false,                    // 收藏夹/自己发送为 true
    "sender": {                      // 频道帖可省略；频道自身可用 kind=channel
      "kind": "user", "id": 355927778, "name": "…", "username": "…",
      "avatar_url": "…"
    },
    "post_author": "…",              // 频道签名，可选
    "text": "…",
    "entities": [ … ],               // 与预览接口同构
    "media": { … },                  // 与预览 media 同构（含 thumbnail_url / preview_url）
    "medias": [ … ],                 // 相册
    "grouped_id": 123,               // 非相册省略
    "reply_to": {"message_id": 8870, "sender_name": "…", "text": "…"},  // 可选
    "forwarded_from": {"name": "…", "peer": "channel:…"},               // 可选
    "views": 1200,                   // 频道消息
    "edited_at": 1763500100,         // 已编辑
    "service": {"action": "pin", "text": "…"},  // 服务消息
    "pinned": false                  // 是否为当前置顶
  }],
  "next_offset_id": 8838,             // 没有更早消息时省略
  "has_more": true
}
```

- `media_only=true` 只返回含媒体的消息；`downloadable=false` 的条目前端置灰并提示原因（超 2GB / 无权限）。
- 封面/预览地址与预览接口共用 token（30 分钟），过期后重新拉消息即可。
- 一期不支持服务端搜索；`query` 由前端在已加载消息里过滤（二期加 `messages.search`）。

#### 2.2.6 置顶消息

```
GET /api/v1/accounts/{id}/chats/{peer}/pinned
→ 200 {"message": {与消息列表 item 同构}}
→ 404 没有置顶消息
```

#### 2.2.7 按消息下载

私聊/收藏夹没有 `t.me` 链接，不能复用 `POST /downloads` 的 `links`：

```
POST /api/v1/downloads/from-messages
body: {
  "account_id": 1,
  "peer": "channel:3713824040",
  "message_ids": [8888, 8889],       // 相册可传多条，或传主消息 ID 由服务端展开
  "overwrite": false
}
→ 201 {同 POST /downloads 的 created/existing/skipped/failed}
```

- 服务端按 peer 重新解析消息，校验媒体与单文件上限；失败项写入 `failed` / `skipped`。
- 一次最多 50 条；超过返回 400。
- 与监控复用 `EnqueueMedia` 路径，不重新走链接解析。

### 2.3 监控

> 以下接口与事件均未实现。监控由 `tg_down serve` 进程常驻执行；这些接口只管理配置。
> `serve` 未运行时配置照常保存，但不会产生监控流量；启动后由进程内的 supervisor 接管。
> 设计细节见 [plan.md §3.2](./plan.md#32-群--频道监控规划)。

#### 2.3.1 监控 CRUD

```
GET /api/v1/monitors?account_id=1
→ 200 {"monitors":[{
  "id": 1,
  "account_id": 1,
  "peer": {"kind": "channel", "id": 3713824040, "title": "…", "username": "…"},
  "trigger": "new",               // new | pin | both
  "enabled": true,
  "status": "running",            // idle | running | lagging | error | paused
  "last_error": "",
  "last_event_at": 1763500000,
  "last_message_id": 2522,
  "match_count": 18,
  "skip_count": 3,
  "filter": { … },                // 见下
  "created_at": 1763500000,
  "updated_at": 1763500120
}]}

POST /api/v1/monitors
body: {
  "account_id": 1,
  "peer": {"link": "https://t.me/example_channel"},
  "trigger": "new",
  "download_enabled": true,       // false 时只归档/供 RSS，不入队下载
  "filter": {
    "media": {"kinds": ["video"], "min_size": 0, "max_size": 0, "min_duration": 0, "max_duration": 0},
    "senders": {"include": ["user:123456", "@alice"], "exclude": ["@spam"]},
    "pinned": "any",              // any | only | exclude
    "text": {"include_any": ["4K"], "exclude": ["预告"]},
    "forward": {"exclude": false, "include_from": []},
    "album": "all"
  },
  "download": {"overwrite": false},
  "backfill_limit": 0,
  "rate_limit": {"max_per_hour": 20}
}
→ 201 {监控对象}
→ 400 bad_request（过滤条件非法、peer 链接无效）
→ 409 conflict（账号没有可用 session / peer 不可访问 / 账号未加入该群或频道）
```

> 监控只支持当前账号**已加入**的群 / 频道；不自动加群、不轮询。未加入时返回 409，提示先加入。

其余接口：

```
PUT    /api/v1/monitors/{id}            → 200 更新后的监控对象
DELETE /api/v1/monitors/{id}            → 200 {"ok":true}
POST   /api/v1/monitors/{id}/enable     → 200 {监控对象}
POST   /api/v1/monitors/{id}/disable    → 200 {监控对象}
```

`peer` 也可以直接给 `{"kind":"channel","id":…,"access_hash":…}` 或 `{"username":"…"}`；
`pinned` 的 `only` 表示「当前置顶」，与 `trigger=pin` 的「置顶动作」不同，见 [plan.md §3.2.3](./plan.md#323-触发与过滤)。

#### 2.3.2 干跑与命中记录

```
POST /api/v1/monitors/{id}/test
body: {"limit": 50}                       // 1-200，默认 50
→ 200 {"items": [{
  "message_id": 2522,
  "date": 1763500000,
  "sender": {"id": 123456, "name": "…", "username": "…"},   // 频道帖子省略
  "media_kind": "video",
  "file_name": "….mp4",
  "file_size": 734003200,
  "matched": true,
  "reason": "media=video; sender included",
  "skipped_reason": ""                    // matched=false 时说明原因
}]}

GET /api/v1/monitors/{id}/matches?limit=50
→ 200 {"items": [{
  "message_id": 2522,
  "sender_id": 123456,
  "media_kind": "video",
  "file_name": "….mp4",
  "file_size": 734003200,
  "task_id": 42,                          // 跳过时省略
  "reason": "matched",
  "skipped_reason": "",
  "matched_at": 1763500000
}]}
```

#### 2.3.3 解析 peer

```
POST /api/v1/monitors/resolve
body: {"link": "https://t.me/example"}    // 或 {"username": "example"}
→ 200 {
  "peer": {"kind": "channel", "id": 3713824040, "title": "…", "username": "…", "broadcast": true},
  "pinned_message_id": 2522                // 没有置顶时省略
}
→ 400 bad_request / 409 conflict / 502 resolve_failed / 504 resolve_timeout
```

#### 2.3.4 SSE 扩展

```
event: monitor.status
data: {"id": 1, "status": "running", "last_error": ""}

event: monitor.match
data: {"monitor_id": 1, "message_id": 2522, "task_id": 42, "matched": true}
```

### 2.4 RSS 输出

> 以下接口均未实现。RSS 由 `serve` 进程提供；`/rss/*` 不走 Bearer token，靠 feed token 鉴权。
> 设计细节见 [plan.md §3.3](./plan.md#33-rss-输出服务规划)。

#### 2.4.1 管理 feed（走 Bearer token）

```
GET /api/v1/monitors/{id}/feeds
→ 200 {"feeds": [{
  "id": 1,
  "mode": "all",                  // all | media | matched；默认 all
  "limit": 50,
  "enclosure": true,              // 是否输出媒体 enclosure；默认 true
  "enabled": true,
  "created_at": 1763500000,
  "last_access_at": 1763500120,
  "last_build_at": 1763500120,
  "updated_at": 1763500120
}]}

POST /api/v1/monitors/{id}/feeds
body: {"mode": "all", "limit": 50, "enclosure": true}
→ 201 {
  "id": 1,
  "url": "http://127.0.0.1:22429/rss/<token>.xml",
  "token": "<只返回一次，请立即保存>"
}

DELETE /api/v1/feeds/{id}        → 200 {"ok":true}

POST /api/v1/feeds/{id}/rotate
→ 200 {
  "url": "http://127.0.0.1:22429/rss/<new-token>.xml",
  "token": "<只返回一次>"
}
```

- 一个监控可以有多条 feed（例如 `all` + `matched`）；不做全局聚合 feed。
- 列表接口不返回 token；创建/轮换后旧 token 立即失效；token 默认永久有效。
- 400 `bad_request`（mode/limit/enclosure 非法）；404 监控不存在。

#### 2.4.2 公开 feed（token 鉴权，不需要 Authorization）

```
GET /rss/{token}.xml?limit=50
→ 200 application/rss+xml; charset=utf-8
   ETag: W/"feed-<id>-<latest_message_id>-<config_version>"
   Last-Modified: <max(最新消息时间, feed.updated_at)>
   Cache-Control: private, max-age=60
→ 304（If-None-Match / If-Modified-Since 命中）
→ 404 token 无效 / feed 已停用
→ 429 超过每 token 限流（默认 60 次/分钟）

GET /rss/{token}/media/{message_id}
→ 200 文件字节（支持 Range）；任务未完成或不存在时 404
→ 404 / 429

GET /rss/{token}/thumb/{message_id}
→ 200 image/jpeg；没有封面 404
→ 404 / 429
```

示例条目（RSS 2.0 + Media RSS）：

```xml
<item>
  <title>某频道 · #2522</title>
  <link>https://t.me/example/2522</link>
  <guid isPermaLink="false">tg:3713824040:2522</guid>
  <pubDate>Fri, 21 Nov 2026 10:00:00 +0800</pubDate>
  <description>…正文与发送人…</description>
  <category>video</category>
  <media:content url="http://host/rss/TOKEN/media/2522" type="video/mp4" fileSize="734003200"/>
  <media:thumbnail url="http://host/rss/TOKEN/thumb/2522"/>
</item>
```

说明：

- feed URL 本身就是凭证，请按密码保管；服务端只存 token 哈希。
- `mode=all` 为默认（文本 + 媒体）；`media` 仅含媒体，`matched` 只包含命中监控并创建任务的消息。
- `enclosure` 默认 `true`，输出已下载文件的 `media:content`；关闭后只保留 `t.me` 链接。
- 只输出 RSS 2.0 + Media RSS，不提供 Atom / JSON Feed。
- 相册 v1 一条消息一个 item，暂不合并成 `media:group`。
- 媒体直链只服务**已完成**的本地下载任务，不代理 Telegram 的临时直链。

### 2.5 下载页草稿与历史

> 以下接口均未实现。设计细节见 [plan.md §3.4](./plan.md#34-下载页输入草稿规划)、
> [§3.5](./plan.md#35-下载输入历史规划)。

#### 2.5.1 输入草稿

下载页的三个输入（`links`、`expand_text_links`、`overwrite`）自动保存到数据库，刷新 / 换浏览器后恢复。
同时缓存一份上次解析摘要（只读展示），但**不自动重新解析**；预览 token 过期后由用户重新解析。

```
GET /api/v1/downloads/draft
→ 200 {
  "links": "",
  "expand_text_links": false,
  "overwrite": false,
  "cache": { … },                 // 可选，上次解析摘要
  "updated_at": 1763500000        // 从未保存时省略
}

PUT /api/v1/downloads/draft
body: {"links":"https://t.me/...\nhttps://t.me/...","expand_text_links":false,"overwrite":false,"cache":{…}}
→ 200 保存后的完整草稿
→ 400 links 超过 256 KiB / 500 行，或字段类型错误
```

- 全量替换输入；没有草稿时返回默认值（200），不是 404。
- `cache` 省略时保留已有缓存（避免自动保存把摘要冲掉），`cache: null` 显式清除。
- `cache` 结构与「输入历史」的缓存一致，≤ 32 KiB；非法时丢弃本次缓存但仍保存输入。
- 前端 debounce 800ms 自动保存；两个开关立即保存；`pagehide` 时尽力补一次。
- 清空 links 会保存空串；不删除行，以保留两个开关状态。
- 一次下载仍只接受 200 条链接；草稿允许更多行，便于先整理再解析。
- 与 `GET /downloads/{id}` 同前缀，静态段 `draft` 必须优先于 `{id}`。

#### 2.5.2 输入历史

保存最近解析过的输入批次，下载页可展示并一键回填。与草稿不同：草稿只有一份、随输入覆盖；
历史保留多份、按最近使用排序。

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
body: {
  "links":"…",
  "expand_text_links":false,
  "overwrite":false,
  "cache": { … }        // 可选；来自预览响应，非法时丢弃缓存但保留输入
}
→ 201 新建；内容重复时 200 并更新 last_used_at / use_count / cache
→ 400 links 超过 256 KiB / 500 行，或字段类型错误

DELETE /api/v1/downloads/history/{id}   → 200 {"ok":true}
DELETE /api/v1/downloads/history        → 200 {"ok":true,"removed":12}
```

- 列表只返回 `summary` / `chats` / `cache_truncated`，不返回完整 `links` 与缓存条目；回填时取详情。
- `cache_truncated` 表示 `summary.total > len(cache.items)`（只缓存前 20 条）。
- 按 `last_used_at` 倒序；`limit` 默认 20，最大 20；只保留最近 20 条。
- 服务端按「换行归一后的 links + 两个开关」哈希去重。
- 前端只在「解析预览」成功后记录；失败静默，不影响解析。
- 静态段 `history` 必须优先于 `{id}` 路由。

缓存字段（服务端白名单，超出部分截断/丢弃）：

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

- 缓存 ≤ 32 KiB；`items` ≤ 20、`chats` ≤ 5、`failures` ≤ 5，文本/错误各 200 字。
- 不缓存缩略图/预览 URL、entities、`file_id`、`access_hash` 等会过期或敏感的字段。
- 缓存只用于历史列表/详情展示；回填仍只恢复输入，不直接用缓存创建下载。

## 3. 代理与网络

- 实际生效代理 = `http_proxy_enabled` 为 true **且** `http_proxy` 非空时使用 `http_proxy`
  （支持 http/https/socks5/socks5h），否则直连。
- 代理同时作用于 MTProto 连接与下载；`http_proxy_enabled=false` 时登录、探测与下载都直连。
  Bot API 接入后同样走这套配置。
- 修改后不重启进程，但运行中任务沿用旧连接，新任务生效。

## 4. 版本演进

- v1 保持路径稳定；新增字段只加不删。
- 未来若做多用户，`token` 中间件替换为会话中间件，路径不变。
