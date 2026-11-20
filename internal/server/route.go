package server

import (
	"net/http"
	"time"

	"github.com/ma6254/tg_down/internal/build"
)

// routes 注册全部 HTTP 路由。
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// 健康检查：前端启动时也用它确认后端已就绪。
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	// 登录探测：等价于 account login --probe（读取本机 Telegram Desktop 的 tdata）。
	mux.HandleFunc("POST /api/v1/accounts/probe", s.handleLoginProbe)

	// 账号列表：等价于 account list。
	mux.HandleFunc("GET /api/v1/accounts", s.handleAccountList)

	// 账号详情：等价于 account info <id>。
	mux.HandleFunc("GET /api/v1/accounts/{id}", s.handleAccountGet)

	// 在线资料：等价于 account info <id> --live（联网查询，不落库）。
	mux.HandleFunc("GET /api/v1/accounts/{id}/live", s.handleAccountLive)

	// 切换当前账号：等价于 account switch <id>。
	mux.HandleFunc("POST /api/v1/accounts/{id}/select", s.handleAccountSelect)

	// 链接解析：等价于 download -u <链接>，不落库、不开始下载。
	mux.HandleFunc("POST /api/v1/links/preview", s.handlePreviewLinks)

	// 媒体缩略图：按解析结果里的 token 下载小图，供列表展示封面。
	mux.HandleFunc("GET /api/v1/media/thumbnail/{token}", s.handleMediaThumbnail)

	// 系统配置：等价于 config list / config set / config unset。
	mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/v1/settings", s.handleSettingsUpdate)

	// 尚未实现的接口明确返回 JSON 404，而不是回落到前端页面，
	// 免得前端把 HTML 当成接口响应。兜底模式不带方法，
	// 因此方法不匹配的请求也会落到这里（404 而非 405）。
	mux.HandleFunc("/api/", handleAPINotFound)

	// 接口文档：/api/openapi.json 与 /swagger/，见 swagger.go。
	s.mountSwagger(mux)

	// 前端按 web.mode 接入，见 web.go。
	s.mountWeb(mux)

	return mux
}

// handleHealth 返回服务状态；uptime 便于确认进程是否刚重启。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"version":        build.Version,
		"uptime_seconds": int64(time.Since(s.startTime).Seconds()),
	})
}

// handleAPINotFound 响应尚未实现的 /api 路径。
func handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "接口不存在: "+r.URL.Path)
}
