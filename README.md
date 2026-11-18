# tg_down_server

Telegram 下载工具的 Go 后端。

把 Telegram 里能看到的媒体按任务队列下载到本地磁盘：单用户、本地优先，默认只监听 `127.0.0.1`；需要对外暴露时再启用 Bearer token。前端是一个 React 单页应用，构建后通过 `go:embed` 打进同一个可执行文件。

> **状态：脚手架阶段（M0）**。目前只有 cobra 命令入口，HTTP 服务、数据库与下载逻辑尚未实现。完整规划见 [规划文档](./doc/plan.md)。

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

当前只有命令行骨架：

```bash
go run .                          # 打印 cobra 帮助
go build -o tg_down.exe .         # 构建
```

服务启动（M1 起）：

```bash
./tg_down.exe                     # 默认 http://127.0.0.1:22429
```

## 配置（规划中）

优先级：命令行参数 > 环境变量（`TG_DOWN_*`）> `config.json` > 内置默认值。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `server.http_addr` | `127.0.0.1:22429` | 监听地址，默认仅本机 |
| `download.dir` | `./downloads` | 下载根目录 |
| `download.max_concurrency` | `3` | 并发下载数 |
| `auth.token` | 空 | 对外暴露时必填 |
| `botapi.base_url` | 官方地址 | 指向自建 local bot-api-server |

## 相关文档

- [整体规划](./doc/plan.md)
- [API 草案](./doc/api.md)
- [参考实现分析](./doc/reference.md)

## 许可证

见 [LICENSE](./LICENSE)。当前文件为空，待补充。
