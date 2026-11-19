package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// defaultProbeTimeout 是登录探测的默认超时，与 CLI 的 --probe-timeout 默认值一致。
	defaultProbeTimeout = 30 * time.Second
	// minProbeTimeout / maxProbeTimeout 限制调用方可以要求的超时范围。
	minProbeTimeout = 5 * time.Second
	maxProbeTimeout = 5 * time.Minute
	// probeBodyLimit 限制请求体大小，探测请求只有几个字段。
	probeBodyLimit = 4 << 10
)

// ProbeRequest 是 POST /api/v1/accounts/probe 的请求体，对应 CLI 的：
//
//	tg_down account login --probe [--tdata …] [--tdata-passcode …] [--probe-timeout …]
type ProbeRequest struct {
	TDataPath     string `json:"tdata,omitempty"`          // Telegram Desktop 的 tdata 目录，留空自动查找
	TDataPasscode string `json:"tdata_passcode,omitempty"` // Telegram Desktop 的本地密码
	TimeoutSecond int    `json:"timeout_seconds,omitempty"`
	// Import 表示是否把探测到的账号写入 sessions/ 与数据库，缺省 true（与 CLI 一致）。
	Import *bool `json:"import,omitempty"`
}

// ProbeResponse 是一次登录探测的结果。
type ProbeResponse struct {
	TDataPath string         `json:"tdata_path,omitempty"`
	Accounts  []ProbeAccount `json:"accounts"`
	Imported  int            `json:"imported"`
	Skipped   int            `json:"skipped"`
	// SelectedAccountID 在导入后库里只剩一个账号、被自动选中时返回。
	SelectedAccountID int64 `json:"selected_account_id,omitempty"`
}

// ProbeAccount 是 tdata 中单个账号的探测结果。
//
// 单个账号失败不影响其他账号：失败原因写在 Error 里，Error 为空表示探测成功。
type ProbeAccount struct {
	Index    uint32        `json:"index"`
	UserID   uint64        `json:"user_id"`
	Name     string        `json:"name,omitempty"`
	Username string        `json:"username,omitempty"`
	Phone    string        `json:"phone,omitempty"`
	Devices  []ProbeDevice `json:"devices,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// ProbeDevice 是账号当前已登录的客户端，用于在界面上展示并辅助判断会话来源。
type ProbeDevice struct {
	Current       bool   `json:"current"`
	AppName       string `json:"app_name,omitempty"`
	AppVersion    string `json:"app_version,omitempty"`
	Platform      string `json:"platform,omitempty"`
	DeviceModel   string `json:"device_model,omitempty"`
	SystemVersion string `json:"system_version,omitempty"`
	Country       string `json:"country,omitempty"`
	CreatedAt     int64  `json:"created_at,omitempty"` // Unix 秒
	ActiveAt      int64  `json:"active_at,omitempty"`  // Unix 秒
}

// handleLoginProbe 处理 POST /api/v1/accounts/probe：
// 探测本机 Telegram Desktop 的登录状态，等价于 account login --probe。
func (s *Server) handleLoginProbe(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ProbeLogin == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "登录探测功能未接入")
		return
	}

	var req ProbeRequest
	if r.Body != nil {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, probeBodyLimit))
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
			return
		}
	}

	timeout := defaultProbeTimeout
	if req.TimeoutSecond > 0 {
		timeout = time.Duration(req.TimeoutSecond) * time.Second
		if timeout < minProbeTimeout || timeout > maxProbeTimeout {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("timeout_seconds 需在 %d-%d 之间", int(minProbeTimeout.Seconds()), int(maxProbeTimeout.Seconds())))
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	resp, err := s.cfg.ProbeLogin(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "probe_timeout",
				fmt.Sprintf("探测超时（%s）：请检查网络或代理配置（config set http_proxy …，并确认 http_proxy_enabled 为 true），或用 timeout_seconds 调大", timeout))
			return
		}
		// tdata 不存在、本地密码错误、没有已登录账号等都属于「改输入即可修复」，
		// 统一按 400 返回，message 直接给可操作的中文提示。
		writeError(w, http.StatusBadRequest, "probe_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
