# tg_down API 草案

所有接口前缀 `/api/v1`，JSON 收发，SSE 用于进度推送。

- 默认只监听 `127.0.0.1`，此时不需要 token。
- 对外暴露（非回环地址）时，除 `GET /health` 外全部要求 `Authorization: Bearer <token>`。
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
GET /health
→ 200 {"status":"ok","version":"0.1.0","auth_required":false}
```

## 客户端初始化

前端启动先探一次，决定是否显示 token 输入页：

```
GET /api/v1/session
→ 200 {"auth_required":true,"authenticated":false}
→ 200 {"auth_required":false,"authenticated":true}
```

## 账号

同一套接口覆盖 MTProto 用户账号与 Bot API 机器人账号，用 `kind` 区分。

### 探测本机 Telegram Desktop

等价于 `tg_down account login --probe`：读取本机 Telegram Desktop 的 tdata，
用其中已有的授权逐个连接 Telegram 校验，默认把有效账号导入 `sessions/` 与数据库。

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
    "id": 1,
    "kind": "mtproto",          // mtproto | botapi
    "name": "+86****1234",      // bot 则为 username
    "username": "ma6254",
    "phone": "8613800000000",   // 不带 + 号，展示时打码
    "state": "authed",          // unauthed | authed | error
    "last_error": "",           // 无错误时省略
    "created_at": 1763500000,   // Unix 秒
    "current": true             // 是否为当前选中账号
  }],
  "current_account_id": 1       // 未选中时省略
}
```

### 切换当前账号

```
POST /api/v1/accounts/{id}/select
→ 200 {"ok":true,"current_account_id":3}
→ 404 {"error":{"code":"not_found","message":"账号不存在: 账号 ID 9 不存在…"}}
→ 409 {"error":{"code":"conflict","message":"账号的 session 文件不可用…"}}
```

切换前会确认账号存在、session 文件可读；结果写入 settings.current_account_id，
`GET /api/v1/accounts` 的 `current` / `current_account_id` 随之变化。

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

### 创建

```
POST /api/v1/downloads
body: {"account_id":1,"chat_id":-100123,"message_id":8888}
→ 201 {"id":42,"status":"pending"}

body: {"items":[{"account_id":1,"chat_id":-100123,"message_id":8888}, ...]}
→ 201 {"created":[42,43],"skipped":[44]}     // 44 已存在
```

`(account_id, chat_id, message_id)` 唯一，重复创建返回已有 id（`skipped`）。

### 列表与详情

```
GET /api/v1/downloads?status=running&limit=100
→ 200 [{
  "id": 42,
  "account_id": 1,
  "chat_id": -100123,
  "chat_title": "某频道",
  "message_id": 8888,
  "file_name": "clip.mp4",
  "file_size": 734003200,
  "status": "running",        // pending|running|paused|done|failed
  "offset": 104857600,
  "speed": 2457600,           // B/s，仅 running
  "error": null,
  "created_at": 1763500000,
  "updated_at": 1763500120
}]

GET /api/v1/downloads/{id}
→ 200 {...同上, "save_path":"D:/downloads/某频道/clip.mp4"}
```

### 控制

```
POST   /api/v1/downloads/{id}/pause    → 200 {"status":"paused"}
POST   /api/v1/downloads/{id}/resume   → 200 {"status":"running"|"pending"}
POST   /api/v1/downloads/{id}/retry    → 200 重置错误、从 offset 继续
DELETE /api/v1/downloads/{id}?delete_file=true
                                       → 200 {"ok":true}
```

说明：
- `pause` 对 `pending` 任务同样生效，直接出队。
- `retry` 仅用于 `failed`；对 `paused` 用 `resume`。
- 删除运行中任务会先取消 context 再清理 `.part`。

## 实时进度（SSE）

```
GET /api/v1/events
Accept: text/event-stream
```

事件类型：

```
event: task.progress
data: {"id":42,"offset":125829120,"size":734003200,"speed":2457600,"eta":248}

event: task.done
data: {"id":42,"save_path":"D:/downloads/某频道/clip.mp4","size":734003200}

event: task.failed
data: {"id":42,"error":{"code":"flood_wait","message":"...","retry_after":120}}

event: account.state
data: {"id":1,"state":"authed"}

event: ping
data: {}
```

- 服务端每 500ms 聚合一次，只为运行中任务推送。
- 前端断线自动重连，并全量 `GET /downloads` 对齐状态。
- `ping` 用于保活，建议 15s 一次。

## 设置

```
GET /api/v1/settings
→ 200 {
  "download_dir": "D:/downloads",
  "max_concurrency": 3,
  "botapi_base_url": "",          // 空 = 官方 https://api.telegram.org
  "proxy": "",                    // socks5://127.0.0.1:1080
  "naming": "chat_dir",           // flat | chat_dir
  "auto_start": true
}

PUT /api/v1/settings
body: {"max_concurrency":2}
→ 200 {完整设置}
```

校验：`max_concurrency` ∈ [1,8]；`download_dir` 必须存在且可写（服务端 `os.Stat` 检查）。

## 代理与网络

- `proxy` 同时作用于 MTProto 连接与 Bot API HTTP 客户端。
- 修改后不重启进程，但运行中任务沿用旧连接，新任务生效。

## 版本演进

- v1 保持路径稳定；新增字段只加不删。
- 未来若做多用户，`token` 中间件替换为会话中间件，路径不变。
