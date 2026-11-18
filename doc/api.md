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

```
GET /api/v1/accounts
→ 200 [{
  "id": 1,
  "kind": "mtproto",          // mtproto | botapi
  "name": "+86****1234",      // bot 则为 username
  "state": "authed",          // unauthed | authed | error
  "last_error": null,
  "created_at": 1763500000
}]
```

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
GET    /api/v1/accounts/{id}      → 单个账号详情
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
