// Package server 是 HTTP 层：路由注册、中间件与前端静态资源接入。
//
// 目录结构参考 omni_toolbox/internal/server：
//
//	server.go      服务生命周期与 JSON 响应
//	route.go       路由表
//	middleware.go  访问日志、panic 恢复、安全头、Bearer 鉴权
//	web.go         前端接入（占位页 / proxy / dir）
//	event_hub.go   SSE 广播器（/api/v1/events 的事件源）
//	thumbnail_tokens.go 缩略图 token 登记与按需下载（/api/v1/media/thumbnail）
//
// 业务逻辑不放在这里：handler 只做参数解析、调用 internal/ 下的服务并写回 JSON。
// 只有两类东西留在本包：为端点服务的传输层运行时状态（事件广播、缩略图 token），
// 以及 HTTP 编解码本身。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ma6254/tg_down/internal/config"
)

// Config 是 HTTP 服务的启动参数。
type Config struct {
	Addr  string    // 监听地址，形如 127.0.0.1:22429
	Token string    // /api/* 的 Bearer token；为空表示只监听回环、免鉴权
	Web   WebConfig // 前端接入方式

	// ProbeLogin 执行「登录探测」业务：读取本机 Telegram Desktop 的 tdata，
	// 校验账号并（可选）导入数据库。由 cmd 注入具体实现，
	// server 只负责 HTTP 编解码；为 nil 时接口返回 501。
	ProbeLogin func(ctx context.Context, req ProbeRequest) (ProbeResponse, error)

	// ListAccounts 返回数据库里的账号列表（含当前选中账号标记）。
	// 为 nil 时接口返回 501。
	ListAccounts func(ctx context.Context) ([]Account, error)

	// SelectAccount 把指定账号设为当前账号。
	// 业务层用 ErrAccountNotFound / ErrAccountNoSession 表示可归因于请求的失败；
	// 为 nil 时接口返回 501。
	SelectAccount func(ctx context.Context, id int64) error

	// GetAccount 返回单个账号的详情。
	// 业务层用 ErrAccountNotFound 表示账号不存在；为 nil 时接口返回 501。
	GetAccount func(ctx context.Context, id int64) (AccountDetail, error)

	// GetAccountLive 联网查询账号的最新资料与已登录客户端（只查资料；
	// 首次联网拿到 user.id 时会补写账号的 telegram_id）。
	// 业务层用 ErrAccountNotFound / ErrAccountNoSession / ErrAccountLiveFailed 表示
	// 可归因的失败；为 nil 时接口返回 501。
	GetAccountLive func(ctx context.Context, id int64) (AccountLiveInfo, error)

	// GetAccountAvatar 返回账号头像（JPEG 字节，带磁盘缓存）。
	// 业务层用 ErrAccountNotFound / ErrAccountNoSession / ErrThumbnailNotFound
	// 表示可归因的失败；为 nil 时接口返回 501。
	GetAccountAvatar func(ctx context.Context, id int64) (MediaThumbnail, error)

	// LogoutAccount 注销账号：尽力吊销 Telegram session，并删除本地记录与文件。
	// 业务层用 ErrAccountNotFound 表示账号不存在；为 nil 时接口返回 501。
	LogoutAccount func(ctx context.Context, id int64) (AccountLogoutResult, error)

	// DeleteAccount 只删除本地账号记录与 session 文件，不吊销 Telegram 会话。
	// 业务层用 ErrAccountNotFound 表示账号不存在；为 nil 时接口返回 501。
	DeleteAccount func(ctx context.Context, id int64) (AccountDeleteResult, error)

	// PreviewLinks 解析 Telegram 消息链接，返回正文、内嵌链接与媒体元数据（不落库）。
	// 业务层用 ErrNoCurrentAccount / ErrAccountNoSession / ErrPreviewFailed 表示
	// 可归因的失败；为 nil 时接口返回 501。
	PreviewLinks func(ctx context.Context, req PreviewRequest) (PreviewResponse, error)

	// GetSettings 返回全部配置项（领域记录，DTO 映射在 server 内完成）；
	// 为 nil 时接口返回 501。
	GetSettings func(ctx context.Context) ([]config.Entry, error)

	// UpdateSettings 应用部分更新（值为 nil 表示删除），返回更新后的全量配置。
	// 业务层用 config.ErrInvalidSetting 表示未知键或取值非法；为 nil 时接口返回 501。
	UpdateSettings func(ctx context.Context, patch map[string]*string) ([]config.Entry, error)

	// CreateDownloads 解析链接、创建下载任务并入队；为 nil 时接口返回 501。
	CreateDownloads func(ctx context.Context, req DownloadCreateRequest) (DownloadCreateResponse, error)

	// ListDownloads 返回任务列表，运行中的任务带实时进度；为 nil 时接口返回 501。
	ListDownloads func(ctx context.Context, query DownloadListQuery) ([]DownloadTask, error)

	// GetDownload 返回单个任务详情；为 nil 时接口返回 501。
	GetDownload func(ctx context.Context, id uint) (DownloadTask, error)

	// ControlDownload 执行 pause / resume / retry；为 nil 时接口返回 501。
	ControlDownload func(ctx context.Context, id uint, action string) (DownloadTask, error)

	// DeleteDownload 删除任务记录，deleteFile 为 true 时同时删除已下载文件；
	// 为 nil 时接口返回 501。
	DeleteDownload func(ctx context.Context, id uint, deleteFile bool) error

	// GetDownloadThumbnail 返回任务卡片用的缩略图；为 nil 时接口返回 501。
	GetDownloadThumbnail func(ctx context.Context, id uint) (MediaThumbnail, error)

	// EventHub 是 SSE 广播器实例，由 cmd 层创建并交给下载管理器发布事件。
	// 为 nil 时 /api/v1/events 返回 501。
	EventHub *EventHub
}

// WebConfig 决定前端如何挂载到 / 上。
type WebConfig struct {
	// Mode 取值：off（不提供前端）、proxy（反向代理到 Vite dev server）、
	// dir（从本地构建目录提供）、留空（用 FS，否则内置占位页）。
	Mode     string
	ProxyURL string // proxy 模式的下游地址
	Dir      string // dir 模式的前端构建目录

	// FS 是内嵌进二进制的前端产物（默认构建生成）；
	// Mode 留空且 FS 非 nil 时直接从它提供页面。
	FS fs.FS
}

// Server 是 HTTP 服务实例。
type Server struct {
	cfg       Config
	mux       *http.ServeMux
	startTime time.Time

	// connCtx 是所有请求 context 的根：Stop 时统一取消，
	// 让 /api/v1/events 这类长连接立即退出，Shutdown 不必干等超时。
	connCtx    context.Context
	connCancel context.CancelFunc

	mu   sync.Mutex
	http *http.Server
}

// New 构造 Server 并注册路由。
func New(cfg Config) *Server {
	connCtx, connCancel := context.WithCancel(context.Background())
	srv := &Server{
		cfg:        cfg,
		startTime:  time.Now(),
		connCtx:    connCtx,
		connCancel: connCancel,
	}
	srv.mux = srv.routes()
	return srv
}

// Handler 返回带日志、panic 恢复、安全头与鉴权的处理器（测试也使用它）。
func (s *Server) Handler() http.Handler {
	return recoverMiddleware(loggingMiddleware(securityHeaders(bearerAuth(s.cfg.Token, s.mux))))
}

// Run 自行侦听配置里的地址并服务，直到 Stop 被调用。
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	return s.Serve(ln)
}

// Serve 在给定 listener 上提供服务，返回 nil 表示被 Stop 正常关闭。
//
// 由调用方负责关闭 listener；cmd 层先 net.Listen 再调用它，
// 这样端口占用等错误能在启动阶段就被翻译成可读提示。
func (s *Server) Serve(ln net.Listener) error {
	httpSrv := &http.Server{
		Handler: s.Handler(),
		// 请求 context 统一挂在 connCtx 下：Stop 取消后，SSE 事件流会立即返回。
		BaseContext:       func(net.Listener) context.Context { return s.connCtx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// 不设置 WriteTimeout：/api/v1/events 这类长连接会被写超时掐断。
	}

	s.mu.Lock()
	s.http = httpSrv
	s.mu.Unlock()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("HTTP 服务退出: %w", err)
	}
	return nil
}

// Stop 优雅关闭 HTTP 服务，等待在途请求结束；超时由调用方的 context 控制。
//
// 先取消所有请求的 context：/api/v1/events 这类 SSE 长连接只在 context 结束时
// 退出，否则 Shutdown 会一直等到超时（表现为 Ctrl+C 后报「优雅关闭超时」）。
func (s *Server) Stop(ctx context.Context) error {
	if s.connCancel != nil {
		s.connCancel()
	}

	s.mu.Lock()
	httpSrv := s.http
	s.mu.Unlock()
	if httpSrv == nil {
		return nil
	}
	return httpSrv.Shutdown(ctx)
}

// ErrorResponse 是统一错误响应体。
type ErrorResponse struct {
	Error APIError `json:"error"`
}

// APIError 描述一个业务错误。
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON 以 JSON 写出响应；接口数据一律不缓存。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 以统一格式写出错误响应。
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: APIError{Code: code, Message: message}})
}
