# 参考实现分析：omni_toolbox

结论：**可直接作为 tg_down 的骨架模板**，但只抄骨架与 UI，业务层（下载/鉴权/进度）必须重写。

- 位置：`C:\Users\mjc\proj\omni_toolbox`
- 结构：`omni_toolbox_server/`（Go）+ `omni_toolbox_web/`（React）+ `参考资料/`（gotty、FlClash、DiscImageStudio 等外部仓库）
- 规模：约 10.2k 行 Go，8 个 `*_test.go`；前端 20+ 页面
- 已构建产物：`release/omni_toolbox.exe`（16MB，单文件）+ `data.db`，说明「embed + build.py」链路在本机确实跑通

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
| 鉴权 | **无**（README 明确「局域网访问请自行加认证」） |
| 流式 | 仅 Ping 的 NDJSON 与终端 WebSocket；进度靠 2s 轮询 |
| 仓库状态 | server 侧尚未提交（全为 untracked），web 侧仅一个初始 commit |

## 2. 可直接复用

### 2.1 后端

| omni_toolbox 源 | 落到 tg_down | 改动 |
|---|---|---|
| [internal/server/web.go](../omni_toolbox/omni_toolbox_server/internal/server/web.go) | `internal/httpapi/web.go` | 改包名与配置字段；`web.mode` 四态原样保留 |
| 同上 `spaFileServer` | 同上 | 逻辑照搬：带扩展名找不到返回 404，`assets/` 长缓存 |
| [internal/server/server.go](../omni_toolbox/omni_toolbox_server/internal/server/server.go) 错误体 | `internal/httpapi/respond.go` | `{error:{code,message}}` 与 [api.md](./api.md) 一致，零改动 |
| [internal/server/middleware.go](../omni_toolbox/omni_toolbox_server/internal/server/middleware.go) | `internal/httpapi/middleware.go` | 日志/panic/安全头照搬；`statusRecorder` 必须保留 `Flush`（SSE 依赖） |
| [internal/config/config.go](../omni_toolbox/omni_toolbox_server/internal/config/config.go) | `internal/config/config.go` | 字段换成 tg_down 的：`download.dir`、`download.max_concurrency`、`auth.token`、`botapi.base_url`、`proxy`；`Load`/`Validate`/`Addr` 结构照搬 |
| [cmd/root.go](../omni_toolbox/omni_toolbox_server/cmd/root.go) | `cmd/tgdown/main.go` | 依赖装配 + 优雅退出 + banner 照搬；子命令可砍到 `serve` / `version` |
| [internal/log/](../omni_toolbox/omni_toolbox_server/internal/log) | `internal/log/` | 整套照搬：分级、tag、access/error 轮转、Windows 控制台处理 |
| [internal/webui/](../omni_toolbox/omni_toolbox_server/internal/webui) | `webembed/` | `go:embed dist.zip` + `zip.Reader`（直接是 `fs.FS`），`-tags webdist` 时启用 |
| [internal/queue/queue.go](../omni_toolbox/omni_toolbox_server/internal/queue/queue.go) | `internal/download/manager.go` | **保留形状**：`Store` 接口、`clone()` 快照、`persistLocked`、`pruneLocked`；状态机与字段重写 |
| [internal/database/](../omni_toolbox/omni_toolbox_server/internal/database) 的表映射写法 | `internal/store/` | 领域对象 ↔ 行对象的 `FromDomain` / `ToDomain` 模式值得保留 |
| [build.py](../omni_toolbox/omni_toolbox_server/build.py) | 项目根 `build.py` | 改路径、模块名、端口；保留 `dev-run` 监视重启与 `--skip-frontend` |

### 2.2 前端

| omni_toolbox 源 | 落到 tg_down | 改动 |
|---|---|---|
| [src/services/index.ts](../omni_toolbox/omni_toolbox_web/src/services/index.ts) | `src/api/client.ts` | 保留 `request<T>` + `ApiError`；**加 token header**；NDJSON 解析改造成 SSE 解析 |
| [src/index.css](../omni_toolbox/omni_toolbox_web/src/index.css) | `src/index.css` | 直接复用（主题变量、`@custom-variant dark`） |
| `src/components/ui/*` | `src/components/ui/*` | 直接复用（button/card/input/select/badge/label/textarea） |
| [src/lib/theme.ts](../omni_toolbox/omni_toolbox_web/src/lib/theme.ts) | `src/lib/theme.ts` | 直接复用 |
| [src/router/index.tsx](../omni_toolbox/omni_toolbox_web/src/router/index.tsx) | `src/router/index.tsx` | 路由换成 accounts/chats/downloads/settings |
| [src/components/Layout.tsx](../omni_toolbox/omni_toolbox_web/src/components/Layout.tsx) | `src/components/Layout.tsx` | 顶栏导航改侧栏（会话树需要纵向空间） |
| [src/pages/Queue/index.tsx](../omni_toolbox/omni_toolbox_web/src/pages/Queue/index.tsx) | `src/pages/Downloads/index.tsx` | 统计卡 + 列表 + 详情侧栏布局可留；**2s 轮询换成 SSE** |
| [src/pages/History/index.tsx](../omni_toolbox/omni_toolbox_web/src/pages/History/index.tsx) | 下载历史 | 分页 + 状态筛选的写法可参考 |
| [src/utils/index.ts](../omni_toolbox/omni_toolbox_web/src/utils/index.ts) | `src/utils/index.ts` | `fmtDateTime` / `fmtDuration` 复用，新增 `fmtBytes` / `fmtSpeed` / `fmtEta` |
| `vite.config.ts` | 同 | 复用 `@` alias 与 `/api` proxy，端口改 22429 |

## 3. 必须重写或新增

| 缺口 | omni_toolbox 现状 | tg_down 要求 |
|---|---|---|
| **鉴权** | 全项目无 `Authorization` / `Bearer`；默认只绑回环 | token 中间件；非回环地址未配 token 时拒绝启动 |
| **进度推送** | `setInterval(load, 2000)` 全量刷新 | SSE 500ms 增量；按任务 id 局部更新，避免整表重渲染 |
| **任务生命周期** | `Cancel` 只允许 `pending`；无 pause/resume；worker 固定 1 | pause / resume / retry / delete，运行中可取消（context 取消） |
| **断点续传** | 无 offset 概念，无 `.part` | offset 周期落库，重启续传 |
| **重启语义** | `MarkInterrupted` 把 pending/running 全标 failed，且 `start()` 只回填历史、不重新入队 | running → pending 保留 offset，启动后自动重新调度 |
| **限流** | 无（工具都是短任务） | 并发信号量 + `FLOOD_WAIT` 挂起到 `retry_at` 再恢复 |
| **Telegram 接入** | 完全无（只有 ping/dns/端口探测） | `internal/tgclient/{mtproto,botapi}` |
| **数据库驱动** | GORM + `mattn/go-sqlite3`（CGO，build.py 未设 `CGO_ENABLED`） | 纯 Go：`glebarez/sqlite`（GORM 驱动）或 `modernc.org/sqlite` |
| **任务 ID / 去重** | `job-<hex>` 随机串，无业务唯一键 | 数字 id + `UNIQUE(account_id, chat_id, message_id)` |
| **字段模型** | `Job.Tool/Input/Fields/Output/Data` 面向文本工具 | 面向文件：`file_name/size/offset/speed/temp_path/save_path` |

## 4. 新增模块的骨架建议

### 4.1 token 中间件（`internal/httpapi/auth.go`）

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

启动时判断：`net.ParseIP(host).IsLoopback()` 为假且 token 为空 → 直接报错退出，而不是悄悄裸奔。

### 4.2 SSE Hub（`internal/httpapi/sse.go`）

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

`Serve` 里必须依次：设 `Content-Type: text/event-stream`、`Cache-Control: no-cache`、`Connection: keep-alive` → `w.(http.Flusher).Flush()` → 15s `ping` 保活。中间件 `statusRecorder` 的 `Flush` 转发是前提（omni 已经实现，照抄）。

### 4.3 download.Manager（保留 omni 的 queue 形状）

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

## 5. 迁移顺序

1. **基座**：拷 `config` / `log` / `webui` / `httpapi`（`web.go`、`server.go`、`middleware.go`、`route.go`）+ `build.py`，改模块名与端口。
2. **跑通空壳**：`go run .` 能出 `/api/v1/health` 与空 SPA；Vite dev 走 `web.proxy` 模式。
3. **前端基座**：拷 `index.css`、`components/ui`、`lib/theme.ts`、`api/client.ts`、`router`，页面先放占位。
4. **持久化**：按 [plan.md](./plan.md) 的 schema 建 `accounts` / `downloads` / `settings`，用纯 Go 驱动。
5. **下载器**：先内存队列 + 假 `Fetch`（写随机字节），把 pause/resume/SSE 打通，再接真 Telegram。
6. **后端接入**：先 Bot API（最简单，token 一填就能跑通全链路）→ 再 MTProto 登录。
7. **安全收尾**：token 中间件 + 路径校验 + 敏感字段不回传。

## 6. 已知坑（逐条都要处理）

1. **CGO**：`gorm.io/driver/sqlite` 依赖 `mattn/go-sqlite3`。omni 靠本机 gcc 编译成功，但这会拖死交叉编译。tg_down 必须验证 `CGO_ENABLED=0 go build ./...` 通过。
2. **端口冲突**：omni 用 28088/38081，同时跑会冲突；tg_down 固定 22429（Vite 5173 或 38081 需错开）。
3. **`statusRecorder` 缺 `Flush` 就会让 SSE 失效**——看似正常但事件被 buffer 住，只在连接断开时一次性吐出。
4. **SPA fallback 的「带点 404」规则**：下载接口路径不要带扩展名特征；`/api/...` 必须在挂载静态前注册，否则会被 SPA 吃掉。
5. **`Close()` 里 `close(m.ch)`**：若仍有 goroutine 在 `Submit` 会 panic。下载管理器建议用 `context` 关闭信号，而不是关 channel。
6. **`Config.Validate` 的 `Addr()`**：host 为空时回落 `127.0.0.1`，但判断"是否回环"不能只看字符串（`0.0.0.0`、`::`、`localhost` 都要用 `net.IP.IsLoopback` + 主机名解析）。
7. **Vite dev server `host: '0.0.0.0'`**：omni 这么配的，本机开发没问题，但别带进生产；tg_down 开发模式建议 `127.0.0.1`。
8. **日志脱敏**：omni 的 access log 只记 method/path/status，可沿用；tg_down 绝不能把 token、session blob 打进日志。
9. **`go:embed` 空包**：忘记先构建前端会得到一个空 tar；`build.py` 的顺序是对的，保留 `--skip-frontend` 时要有明确提示。
10. **前端全量刷新**：2s 轮询在 50+ 任务时明显掉帧；SSE 方案下仍要用 `React.memo` 行组件 + 按 id 合并。
11. **`omni_toolbox_server.zip`（30MB）** 是旧打包产物，别当源码参考。
12. **omni 的 `__pty` 子命令**（`ptyhelper`）是终端专用，与 tg_down 无关，不要带过来。

## 7. 对 plan.md 的修订建议

| plan.md 原方案 | 参考 omni 后的调整 |
|---|---|
| `modernc.org/sqlite` | 若沿用 GORM，用 `glebarez/sqlite`（底层同为 modernc，仍是纯 Go）；若不用 ORM，`modernc.org/sqlite` 直接写 SQL |
| `webembed/embed.go` 用 `//go:embed dist` | 改为 `dist.zip` + `zip.Reader`（直接实现 `fs.FS`），由默认构建生成并加 `-tags webdist` |
| 前端状态用 TanStack Query | 可先用 omni 式的「`api` 封装 + `useState` + SSE 推送」，少一层依赖；任务多了再引 Query |
| 里程碑 M0 0.5–1 天 | 有模板可抄，M0 可压到半天；省下的时间投到 M3/M4 的续传与限流 |

## 8. 一句话总结

`omni_toolbox` 提供的是**已验证的工程外壳**（构建、嵌入、配置、日志、UI kit、任务页布局），不提供**下载工具的灵魂**（鉴权、进度流、可恢复的长任务）。按本文第 5 节顺序搬，M0–M2 能省 2–3 天，M3/M4 仍需自己啃。
