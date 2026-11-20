# tg_down API 草案

所有接口前缀 `/api/v1`，JSON 收发，SSE 用于进度推送。

- 默认只监听 `127.0.0.1`，此时不需要 token。
- 对外暴露（非回环地址）时，除静态资源与 `/api/openapi.json` 外，所有 `/api/*`（**包括 health**）都要求 `Authorization: Bearer <token>`。
- 单用户模型，没有用户注册与登录会话；token 即身份。

## 通用

### 错误格式

```json
{
  "error": {
    "code": "flood_wait",
    "message": "Telegram 要求等待 120 秒",
    "retry_after": 120
  }
}
```

| HTTP | code | 含义 |
|---|---|---|
| 400 | `bad_request` | 参数缺失/类型错 |
| 401 | `unauthorized` | token 缺失或错误 |
| 403 | `forbidden` | 非回环且未开 token（启动期拒绝） |
| 404 | `not_found` | 账号/任务/聊天不存在 |
| 409 | `conflict` | 重复入队、账号未登录 |
| 422 | `flood_wait` | Telegram 限流，`retry_after` 秒 |
| 500 | `internal` | 其他服务端错误 |

### 健康检查

```
GET /api/v1/health
→ 200 {"status":"ok","version":"0.1.0","uptime_seconds":123}
```

启用 token 后该接口同样要求 Bearer token；`uptime_seconds` 便于确认进程是否刚重启。

### 系统信息

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

## 客户端初始化

没有匿名的 `/session` 探测接口；前端直接用 health 判断：

- 200：后端可用（免鉴权，或已有 token 有效）
- 401：需要 token，展示登录页
- 网络错误 / 5xx：展示离线页并允许重试

## 账号

同一套接口覆盖 MTProto 用户账号与 Bot API 机器人账号，用 `kind` 区分。

### 探测本机 Telegram Desktop

等价于 `tg_down account login --probe`：读取本机 Telegram Desktop 的 tdata，
用其中已有的授权逐个连接 Telegram 校验，默认把有效账号导入
`<数据目录>/sessions/`（`~/.tg_down/sessions/`）与数据库。

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

### 切换当前账号

```
POST /api/v1/accounts/{id}/select
→ 200 {"ok":true,"current_account_id":3}
→ 404 {"error":{"code":"not_found","message":"账号不存在: 账号 ID 9 不存在…"}}
→ 409 {"error":{"code":"conflict","message":"账号的 session 文件不可用…"}}
```

切换前会确认账号存在、session 文件可读；结果写入 settings.current_account_id，
`GET /api/v1/accounts` 的 `current` / `current_account_id` 随之变化。

### 退出登录

```
POST /api/v1/accounts/{id}/logout
→ 200 {"ok":true,"revoked":true,"removed_session":false}
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}
→ 504 {"error":{"code":"logout_timeout","message":"登出超时，请稍后重试"}}
```

- 先尽力向 Telegram 发送 `auth.logOut` 吊销 session；`revoked=true` 表示吊销成功。
- 网络失败不阻断本地清理，此时返回 `revoked=false`，前端会提示“本地已删除，Telegram 端注销失败”。
- 随后删除数据库记录与 session 文件；删除当前账号时会清空当前选择。
- 已下载的文件不受影响。

### 删除账号（仅本地）

```
DELETE /api/v1/accounts/{id}
→ 200 {"ok":true,"removed_session":true}
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}
```

- 只删除本地数据库记录、session 文件与头像缓存，**不会**向 Telegram 注销会话。
- 桌面导入的账号与 Telegram Desktop 共用登录，用这个接口只移除本地副本，桌面端不受影响。
- 需要同时注销 Telegram 会话时用上面的 `logout`。

### Bot 账号接入

```
POST /api/v1/accounts/bot
body: {"name":"my_bot","token":"123456:ABC..."}
→ 201 {"id":2,"kind":"botapi","name":"my_bot","state":"authed"}
```

服务端会调用 `getMe` 校验；token 非法返回 400 `bad_request`，不落库。

### MTProto 登录（三步）

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

> 登录成功后 session 持久化到库，重启免登录。后续可加 `POST /api/v1/accounts/mtproto/qr` 与 `GET .../qr/poll`（阶段内可选）。

### 查看 / 登出

```
GET /api/v1/accounts/{id}
→ 200 {
  "id": 1,
  "kind": "mtproto",
  "name": "...",
  "username": "ma6254",
  "phone": "8613800000000",
  "state": "authed",
  "created_at": 1763500000,
  "updated_at": 1763500120,      // 从未更新时省略
  "current": true,
  "session_present": true        // session 文件是否还在，false 说明需要重新登录
}
→ 400 账号 ID 不是正整数
→ 404 {"error":{"code":"not_found","message":"账号不存在…"}}

DELETE /api/v1/accounts/{id}      → 200 {"ok":true}   清 session/token
```

### 在线资料（--live）

等价于 `tg_down account info <id> --live`：临时连一次 Telegram，读取最新资料
（名称、会员、生日、简介）与已登录客户端。只查询、不写数据库，单次最长 30 秒。

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

### 账号头像

```
GET /api/v1/accounts/{id}/avatar
→ 200 image/jpeg
```

- 头像字节缓存在 `<数据目录>/avatar/<user_id>.jpg`（默认 `~/.tg_down/avatar/`），
  文件名是 Telegram 的 user.id，换手机号/用户名不会变；旧版本按本地账号 ID
  命名的文件会在首次访问时自动迁移到 user_id 下。
- 群聊、频道与其他用户的头像也缓存在同一目录，命名为
  `peer-<kind>-<id>-<photoID>.jpg`（预览页头像与账号头像共用这个目录）。
- 有效期 24 小时；到期后联网刷新，失败时继续用旧文件兜底。
- 账号没有头像（或被隐藏）时返回 404，前端回退到名称首字符。
- 没有 session 且无缓存时返回 409；联网失败返回 502、超时 504。
- 浏览器侧缓存 1 小时。

## 链接解析

等价于 CLI 的 `tg_down download -u <链接>`：解析链接指向的消息，返回正文、
正文里的文字链接与媒体元数据。只联网读取，不写数据库、不创建下载任务。

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

### 媒体缩略图

```
GET /api/v1/media/thumbnail/{token}
→ 200 image/jpeg
```

`token` 来自解析结果的 `media.thumbnail_url`，有效期 30 分钟；服务端按需下载并缓存，
前端用 `loading="lazy"` 只加载可见的封面。没有缩略图的媒体（如音频、PDF）不会返回该字段。
媒体缩略图字节写盘到 `<数据目录>/thumbnail/`（按媒体身份分片）；
群聊/频道/发送者头像写盘到 `<数据目录>/avatar/peer-<kind>-<id>-<photoID>.jpg`。
token 过期后重新预览仍可直接命中；token 失效返回 404，下载失败返回 502；前端应退回类型图标。

说明：

- 单条链接失败只写在该条的 `error` 字段，整体仍返回 200；`links` 为空或超过
  200 条返回 400。
- 纯文本消息没有 `media`，正文与链接照常返回。
- 相册消息（多图/图文混合）会额外返回 `medias`：每条媒体带自己的 `message_id`
  与 `link`，前端按这个列表逐条展示和入队；`media` 仍保留为链接指向的那一条。
  `POST /downloads` 传相册链接时，服务端会为每条媒体各建一个任务。
- `expand_text_links=true` 时，展开出的条目带 `expanded: true`，并计入
  `summary.expanded`；展开上限 200 条（超过时 `summary.truncated=true`），
  正文里不是消息链接的条数计入 `summary.skipped_links`。
- 错误：400 `bad_request`；409 `conflict`（没有当前账号 / session 不可用）；
  502 `preview_failed`（连接或 Telegram 报错）；504 `preview_timeout`。

## 会话与消息

```
GET /api/v1/accounts/{id}/chats?limit=100&query=
→ 200 [{
  "id": -1001234567890,
  "title": "某频道",
  "username": "somechannel",
  "kind": "channel",          // channel | group | user
  "has_media": true            // 可选：是否值得点进去
}]
```

```
GET /api/v1/accounts/{id}/chats/{chat_id}/messages?offset_id=0&limit=50&media_only=true
→ 200 {
  "items": [{
    "message_id": 8888,
    "date": 1763500000,
    "caption": "...",
    "media": {
      "kind": "video",             // photo | video | audio | document
      "file_name": "clip.mp4",
      "size": 734003200,
      "mime": "video/mp4",
      "file_id": "BQAD...",       // botapi 用
      "dc_id": 4,                  // mtproto 用
      "access_hash": 123456789,    // mtproto 用
      "downloadable": true         // 超出单文件上限时为 false
    }
  }],
  "next_offset_id": 8838,
  "has_more": true
}
```

- `media_only=true` 时只返回含媒体的消息。
- `downloadable=false` 的条目前端置灰并提示原因（超 2GB / 无权限）。

## 下载任务

任务由后台 worker 执行，状态落库；服务重启时残留的 `running` 任务会转为 `paused`，
`pending` 任务自动重新入队。进度通过 SSE 推送，未连接时回退到列表轮询。

### 创建

```
POST /api/v1/downloads
body: {
  "links":["https://t.me/JPD233/2522","https://t.me/JPD233/2522?comment=12454"],
  "overwrite": false            // 可选；true 时覆盖同名文件并从零下载
}
→ 201 {
  "created":  [42],                       // 新建并入队的任务
  "existing": [41],                       // 之前已存在（同一链接），不重复下载
  "skipped":  [{"link":"…","reason":"该消息没有可下载的媒体"}],
  "failed":   [{"link":"…","error":"链接无法解析"}]
}
```

- 链接唯一：同一个链接重复创建返回 `existing`，不会重复下载。
- `overwrite=true`：目标位置已有同名文件时直接替换（不再生成「名称 (1).ext」），
  并丢弃已有 `.part` 从零下载；默认 `false` 时同名文件加序号，同名同大小视为已完成。
  该标记随任务落库，任务详情里的 `overwrite` 字段可用于核对。
- 没有媒体或超过账号单文件上限的链接进 `skipped`；解析/查询失败的进 `failed`，
  整体仍返回 201。
- 一次最多 200 条链接。
- 错误：400 `bad_request`；409 `conflict`（没有当前账号 / session 不可用）；
  502 `download_failed`（连接或 Telegram 报错）；504 `download_timeout`。

### 列表与详情

```
GET /api/v1/downloads?status=running&limit=100
→ 200 [{
  "id": 42,
  "link": "https://t.me/JPD233/2522",
  "account_id": 1,
  "chat_id": 3713824040,
  "chat_title": "某频道",
  "message_id": 2522,
  "file_name": "clip.mp4",
  "file_size": 734003200,
  "kind": "video",              // photo | video | audio | document
  "mime": "video/mp4",
  "status": "running",          // pending|running|paused|done|failed
  "offset": 104857600,          // 已下载字节数
  "speed": 2457600,             // B/s，仅 running
  "error": "",
  "save_path": "downloads/某频道/clip.mp4",
  "created_at": 1763500000,
  "updated_at": 1763500120
}]

GET /api/v1/downloads/{id}      → 200 同上；400 ID 非法；404 不存在
```

- `status` 只接受五个状态值；`limit` 范围 1-200，默认 100。
- `running` 的 `offset`/`speed` 来自内存实时进度，其余状态取数据库。

### 控制

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

### 任务缩略图

```
GET /api/v1/downloads/{id}/thumbnail
→ 200 image/jpeg
```

- 任务列表里的 `thumbnail_url` 是创建时登记的封面地址（内存 token，约 30 分钟有效），
  `preview_url` 是更大尺寸的预览图，图片查看器优先用它；该地址失效或历史任务没有地址时，
  前端改用本接口。
- 封面字节缓存在 `<数据目录>/thumbnail/<两位分片>/<sha256>.jpg`（`TG_DOWN_HOME` 下，
  默认 `~/.tg_down/thumbnail`）。缓存键由 `媒体类型 + 文件 ID + 尺寸类型` 决定，
  与 token、进程无关：预览页和任务卡片共享同一份文件，重启后仍能命中。
- 本接口首次访问会重新解析该任务的消息、下载封面并写盘，同时把缓存键记到任务上；
  之后同一任务直接读文件，不再联网。没有可用封面时返回 404。
- 一次首屏可能同时加载多行，服务端最多并发抓取 3 张，避免打开过多 Telegram 连接。
- 旧版本写在 `download_tasks.thumbnail` 列里的 BLOB 已不再使用；服务启动时的迁移会自动
  `DROP COLUMN` 删除该列（数据只删这一列，任务行保留）。SQLite 不会自动缩小文件，
  需要回收磁盘空间可手动执行一次 `VACUUM`。

### 尚未实现

- 下载完成后的大小校验与 CDN 分块哈希校验。

## 实时进度（SSE）

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

## 设置

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
- `default` 是未配置时使用的默认值；目前只有 `http_proxy_enabled` 有默认值 `"true"`。
- 服务启动时如果 `settings` 表为空，会写入带非空默认值的配置项
  （当前只有 `http_proxy_enabled=true`）；空默认值表示未配置，不会落库。
- 只允许写 `config.items` 里登记的键；`current_account_id` 只读，由账号接口维护。
- **代理密码脱敏**：`http_proxy` 的密码在响应里固定返回 `***`，不会回传明文。
  提交时如果密码仍是 `***`，服务端沿用已保存的密码（Web 可以只改主机/端口而不重输密码）；
  要更换密码请提交完整 URL。
- **代理开关**：`http_proxy_enabled` 是布尔值，未配置时视为 `true`；
  设为 `false` 后即使 `http_proxy` 有值也直连，代理地址本身保留。
  清除该键会恢复默认（启用）。
- 清除代理：`{"http_proxy": null}`。
- 未知键、非法值、只读键返回 400 `bad_request`。

## 代理与网络

- 实际生效代理 = `http_proxy_enabled` 为 true **且** `http_proxy` 非空时使用 `http_proxy`（支持 http/https/socks5/socks5h），否则直连。
- 代理同时作用于 MTProto 连接与 Bot API HTTP 客户端；`http_proxy_enabled=false` 时登录与探测也直连。
- 修改后不重启进程，但运行中任务沿用旧连接，新任务生效。

## 版本演进

- v1 保持路径稳定；新增字段只加不删。
- 未来若做多用户，`token` 中间件替换为会话中间件，路径不变。
