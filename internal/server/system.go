package server

import (
	"net/http"
	"runtime"
	"time"

	"github.com/ma6254/tg_down/internal/build"
)

// SystemInfo 是 GET /api/v1/system 的响应：编译期注入的版本信息与运行环境。
//
// version / commit / build_time 由构建脚本用 -ldflags 注入，
// 直接 `go build` 时分别是 dev / none / unknown。
type SystemInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`     // git 提交（短哈希或 tag）
	BuildTime string `json:"build_time"` // 编译时间，RFC3339
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// UptimeSeconds 是进程已运行的秒数，便于确认服务是否刚重启。
	UptimeSeconds int64 `json:"uptime_seconds"`
}

// handleSystemInfo 处理 GET /api/v1/system：返回编译时间、git 提交与运行环境。
func (s *Server) handleSystemInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, SystemInfo{
		Version:       build.Version,
		Commit:        build.Commit,
		BuildTime:     build.BuildTime,
		GoVersion:     build.GoVersion(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	})
}
