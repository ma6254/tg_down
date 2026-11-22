# 参考实现分析：omni_toolbox

> **历史参考（2026-11-18）**：本文比较 `omni_toolbox` 与本项目开工时的设计。
> “落到 tg_down”一列已按当前代码校正（`internal/server/`、`internal/database/`、`cmd/*`；
> `internal/tgclient` 只有 `mtproto`），但“可复用 / 必须重写”的判断仍属于当时的建议。
> 当前项目结构以 [plan.md](./plan.md) 与代码为准，不要按本文的路径或代码片段照搬。

**结论**：`omni_toolbox` 是可用的工程外壳（构建、嵌入、配置、日志、UI kit、任务页布局），
但不提供下载工具的核心能力（鉴权、进度流、可恢复的长任务）。只抄骨架与 UI，业务层重写。

- 位置：`C:\Users\mjc\proj\omni_toolbox`
- 结构：`omni_toolbox_server/`（Go）+ `omni_toolbox_web/`（React）+ `参考资料/`
- 规模：约 10.2k 行 Go、8 个 `*_test.go`；前端 20+ 页面
- 产物：`release/omni_toolbox.exe`（16MB，单文件）+ `data.db`，验证过「embed + build.py」链路

## 1. 事实基线

| 项 | 值 |
|---|---|
| Go | 1.27.1，`module github.com/ma6254/omni-toolbox` |
| 后端依赖 | cobra、gorm + gorm sqlite、gorilla/websocket、go-pty |
| 前端 | React 19.3、Vite 8.3、TypeScript 6、Tailwind 4.3（`@tailwindcss/vite`） |
| UI | shadcn（base-ui primitives）+ `tw-animate-css` + Geist 字体 + 亮/暗主题变量 |
| 路由 | react-router-dom 7，`BrowserRouter` + 后端 SPA fallback |
| 端口 | 后端 28088，Vite dev 38081 |
| 持久化 | SQLite（GORM），只存队列历史 |
| 鉴权 | 无（README 明确「局域网访问请自行加认证」） |
| 流式 | 仅 Ping 的 NDJSON 与终端 WebSocket；进度靠 2s 轮询 |
| 仓库状态 | server 侧尚未提交，web 侧仅一个初始 commit（历史快照） |

## 2. 可直接复用

### 2.1 后端

| omni_toolbox 源 | 落到 tg_down | 改动 |
|---|---|---|
| [internal/server/web.go](../../../omni_toolbox/omni_toolbox_server/internal/server/web.go) | `internal/server/web.go` | 改包名与配置字段；`web.mode` 四态保留 |
| 同上 `spaFileServer` | 同上 | 带扩展名找不到返回 404，`assets/` 长缓存 |
| [internal/server/server.go](../../../omni_toolbox/omni_toolbox_server/internal/server/server.go) 错误体 | `internal/server/server.go` | `{error:{code,message}}` 与 [api.md](./api.md) 一致 |
| [internal/server/middleware.go](../../../omni_toolbox/omni_toolbox_server/internal/server/middleware.go) | `internal/server/middleware.go` | 日志/panic/安全头；`statusRecorder` 必须保留 `Flush`（SSE 依赖） |
| [internal/config/config.go](../../../omni_toolbox/omni_toolbox_server/internal/config/config.go) | `internal/config/config.go` | 换成 settings：`http_proxy`、`http_proxy_enabled`、`api_id`、`api_hash`、`api_custom_enabled`、`current_account_id` |
| [cmd/root.go](../../../omni_toolbox/omni_toolbox_server/cmd/root.go) | `main.go` + `cmd/` | 依赖装配 + 优雅退出 + banner；子命令收敛到 `serve` / `version` |
| [internal/log/](../../../omni_toolbox/omni_toolbox_server/internal/log) | 未单独抽包 | 实际使用标准库 `log`；分级/tag/轮转没有搬 |
| [internal/webui/](../../../omni_toolbox/omni_toolbox_server/internal/webui) | `webembed/` | `go:embed dist.zip` + `zip.Reader`（`fs.FS`），`-tags webdist` 时启用 |
| [internal/queue/queue.go](../../../omni_toolbox/omni_toolbox_server/internal/queue/queue.go) | `internal/download/manager/manager.go` | 保留 `Store` 接口、`clone()` 快照、`persistLocked`、`pruneLocked` 形状；状态机与字段重写 |
| [internal/database/](../../../omni_toolbox/omni_toolbox_server/internal/database) 表映射写法 | `internal/database/` | `FromDomain` / `ToDomain` 模式保留 |
| [build.py](../../../omni_toolbox/omni_toolbox_server/build.py) | 项目根 `build.py` | 改路径、模块名、端口；保留 `dev-run` 与 `--skip-frontend` |

### 2.2 前端

| omni_toolbox 源 | 落到 tg_down | 改动 |
|---|---|---|
| [src/services/index.ts](../../../omni_toolbox/omni_toolbox_web/src/services/index.ts) | `src/api/client.ts` | 保留 `request<T>` + `ApiError`；加 token header；NDJSON 改 SSE |
| [src/index.css](../../../omni_toolbox/omni_toolbox_web/src/index.css) | `src/index.css` | 主题变量、`@custom-variant dark` |
| `src/components/ui/*` | `src/components/ui/*` | 直接复用（button/card/input/select/badge/label/textarea） |
| [src/lib/theme.ts](../../../omni_toolbox/omni_toolbox_web/src/lib/theme.ts) | `src/lib/theme.tsx` | 直接复用 |
| [src/router/index.tsx](../../../omni_toolbox/omni_toolbox_web/src/router/index.tsx) | `src/router.tsx` | 路由换成 accounts/downloads/settings |
| [src/components/Layout.tsx](../../../omni_toolbox/omni_toolbox_web/src/components/Layout.tsx) | `src/components/layout/AppShell.tsx` | 顶栏改侧栏，拆成 AppShell / Sidebar / Topbar / PageHeader |
| [src/pages/Queue/index.tsx](../../../omni_toolbox/omni_toolbox_web/src/pages/Queue/index.tsx) | `src/pages/downloads/index.tsx` | 链接解析 + 任务列表 + 详情；2s 轮询换 SSE |
| [src/pages/History/index.tsx](../../../omni_toolbox/omni_toolbox_web/src/pages/History/index.tsx) | 下载历史 | 分页 + 状态筛选写法可参考 |
| [src/utils/index.ts](../../../omni_toolbox/omni_toolbox_web/src/utils/index.ts) | `src/lib/format.ts` | `fmtDateTime` / `fmtDuration` 复用，新增 `fmtBytes` / `fmtSpeed` / `fmtEta` |
| `vite.config.ts` | 同 | `@` alias 与 `/api` proxy，端口改 22429 |

## 3. 必须重写或新增

| 缺口 | omni_toolbox 现状 | tg_down 要求 |
|---|---|---|
| 鉴权 | 全项目无 `Authorization` / `Bearer`；默认只绑回环 | token 中间件；非回环未配 token 时拒绝启动 |
| 进度推送 | `setInterval(load, 2000)` 全量刷新 | SSE 约 1.5s 增量；按任务 id 局部更新 |
| 任务生命周期 | `Cancel` 只允许 `pending`；无 pause/resume；worker 固定 1 | pause / resume / retry / delete，运行中可取消 |
| 断点续传 | 无 offset、无 `.part` | offset 周期落库，重启续传 |
| 重启语义 | pending/running 全标 failed，且不重新入队 | running → paused 保留 offset，pending 自动重新入队（已实现） |
| 限流 | 无（工具都是短任务） | 并发信号量 + `FLOOD_WAIT` 挂起到 `retry_at`（自动恢复**未实现**） |
| Telegram 接入 | 完全无（只有 ping/dns/端口探测） | `internal/tgclient/mtproto`（Bot API 尚未实现） |
| 数据库驱动 | GORM + `mattn/go-sqlite3`（CGO） | 纯 Go：`glebarez/sqlite` |
| 任务 ID / 去重 | `job-<hex>` 随机串，无业务唯一键 | 数字 id + `link` 唯一 |
| 字段模型 | `Job.Tool/Input/Fields/Output/Data` 面向文本工具 | 面向文件：`file_name/size/offset/speed/save_path` |

## 4. 可借鉴骨架

> 以下代码是**历史示意**，不是当前实现的逐字复制；实际字段与接口以
> `internal/server`、`internal/download/manager` 为准。

### 4.1 token 中间件

```go
func TokenAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "token 无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

启动时判断：`net.ParseIP(host).IsLoopback()` 为假且 token 为空 → 直接报错退出。

### 4.2 SSE Hub

```go
type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// Broadcast 不阻塞：慢客户端丢事件，靠前端重连后的全量 GET 对齐。
func (h *Hub) Broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
```

`Serve` 里依次设置 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、
`Connection: keep-alive` → `Flush()` → 15s `ping` 保活。中间件 `statusRecorder` 的
`Flush` 转发是前提。

### 4.3 download.Manager

```go
type Store interface {
	Save(Task) error
	LoadRunnable() ([]Task, error) // 重启后待恢复：pending + 中断的 running
}

type Task struct {
	ID       int64
	AccountID int64
	ChatID    int64
	MessageID int
	FileName  string
	FileSize  int64
	Status    Status // pending|running|paused|done|failed
	Offset    int64
	Speed     int64
	RetryAt   *time.Time
	Error     string
}

func (m *Manager) Enqueue(src Source) (*Task, error) // 去重
func (m *Manager) Pause(id int64) error
func (m *Manager) Resume(id int64) error
func (m *Manager) Retry(id int64) error
func (m *Manager) Cancel(id int64) error   // 取消运行中：cancel context + 清理 .part
```

> 实际实现的 `Task` 没有 `RetryAt`（FloodWait 自动恢复未做），字段与接口以
> `internal/download/manager` 为准。

## 5. 迁移顺序（历史）

1. **基座**：拷 `config` / `webui` / `server`（`web.go`、`server.go`、`middleware.go`、`route.go`）+ `build.py`，改模块名与端口。
2. **跑通空壳**：`go run .` 能出 `/api/v1/health` 与空 SPA；Vite dev 走 `web.proxy` 模式。
3. **前端基座**：拷 `index.css`、`components/ui`、`lib/theme.tsx`、`api/client.ts`、`router.tsx`，页面先放占位。
4. **持久化**：按 [plan.md](./plan.md) 的模型建 `accounts` / `download_tasks` / `settings`，用纯 Go 驱动。
5. **下载器**：先内存队列 + 假 `Fetch`（写随机字节），把 pause/resume/SSE 打通，再接真 Telegram。
6. **后端接入**：接 Telegram（实际先做了 MTProto；Bot API 尚未接入）。
7. **安全收尾**：token 中间件 + 路径校验 + 敏感字段不回传。

> 实际落地顺序：先 MTProto（登录、探测、解析、缩略图、下载），再做 Web 与内嵌；
> Bot API 尚未开始。

## 6. 已知坑

1. **CGO**：`gorm.io/driver/sqlite` 依赖 `mattn/go-sqlite3`，拖死交叉编译。必须验证 `CGO_ENABLED=0 go build ./...` 通过。
2. **端口冲突**：omni 用 28088/38081；tg_down 固定 22429（Vite 5173 或 38081 需错开）。
3. **`statusRecorder` 缺 `Flush` 会让 SSE 失效**：事件被 buffer 住，连接断开才一次性吐出。
4. **SPA fallback 的「带点 404」规则**：下载接口路径不要带扩展名特征；`/api/...` 必须在挂载静态前注册。
5. **`Close()` 里 `close(m.ch)`**：仍有 goroutine 在 `Submit` 会 panic；用 `context` 关闭信号。
6. **`Config.Validate` 的 `Addr()`**：host 为空时回落 `127.0.0.1`，但判断回环不能只看字符串（`0.0.0.0`、`::`、`localhost` 都要处理）。
7. **Vite dev server `host: '0.0.0.0'`**：开发可以，别带进生产；tg_down 用 `127.0.0.1`。
8. **日志脱敏**：access log 只记 method/path/status；绝不能把 token、session blob 打进日志。
9. **`go:embed` 空包**：忘记先构建前端会得到空包；`build.py` 的顺序要对，`--skip-frontend` 要有提示。
10. **前端全量刷新**：2s 轮询在 50+ 任务时掉帧；SSE 下仍要用 `React.memo` 行组件 + 按 id 合并。
11. **`omni_toolbox_server.zip`（30MB）** 是旧打包产物，别当源码参考。
12. **omni 的 `__pty` 子命令**（`ptyhelper`）是终端专用，与 tg_down 无关。

## 7. 对当前规划的修订（历史）

| plan.md 原方案 | 参考 omni 后的调整 |
|---|---|
| `modernc.org/sqlite` | 若沿用 GORM，用 `glebarez/sqlite`（底层同为 modernc，纯 Go）；否则直接写 SQL |
| `webembed/embed.go` 用 `//go:embed dist` | 改为 `dist.zip` + `zip.Reader`，由默认构建生成并加 `-tags webdist` |
| 前端状态用 TanStack Query | 可先用「`api` 封装 + `useState` + SSE」，任务多了再引 Query |
| 里程碑 M0 0.5–1 天 | 有模板可抄，M0 可压到半天；省下的时间投到 M3/M4 的续传与限流 |

> 最终落地：`glebarez/sqlite`、`dist.zip`、TanStack Query 均已采用；里程碑估算已由
> [plan.md §4](./plan.md#4-路线图) 取代，本文不再维护进度。
