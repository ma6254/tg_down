package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// ErrSettingInvalid 表示配置项未知、值不合法或只读，handler 据此返回 400。
var ErrSettingInvalid = errors.New("配置项不可写")

// Setting 是一个配置项。
//
// Value 是展示值：http_proxy 的密码会被脱敏为 ***，不会回传明文。
// Type 是值的逻辑类型（string / integer / boolean），由后端配置定义决定。
type Setting struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Known       bool   `json:"known"`               // 是否为程序认识的配置项
	ReadOnly    bool   `json:"read_only,omitempty"` // 只能由专门接口维护
}

// SettingsResponse 是 GET / PUT /api/v1/settings 的响应。
type SettingsResponse struct {
	Settings []Setting `json:"settings"`
}

// SettingsPatch 是 PUT /api/v1/settings 的请求体：值为 null 表示删除该配置项，
// 未出现的键保持原值。
type SettingsPatch map[string]*string

// settingsBodyLimit 限制请求体大小，配置项都是短字符串。
const settingsBodyLimit = 16 << 10

// handleSettingsGet 处理 GET /api/v1/settings：返回全部配置项。
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetSettings == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "配置读取功能未接入")
		return
	}

	settings, err := s.cfg.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "读取配置失败: "+err.Error())
		return
	}
	if settings == nil {
		settings = []Setting{}
	}
	writeJSON(w, http.StatusOK, SettingsResponse{Settings: settings})
}

// handleSettingsUpdate 处理 PUT /api/v1/settings：部分更新并返回更新后的全量配置。
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.UpdateSettings == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "配置写入功能未接入")
		return
	}

	var patch SettingsPatch
	if r.Body != nil {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, settingsBodyLimit))
		if err := dec.Decode(&patch); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
			return
		}
	}
	if patch == nil {
		patch = SettingsPatch{}
	}

	settings, err := s.cfg.UpdateSettings(r.Context(), patch)
	if err != nil {
		if errors.Is(err, ErrSettingInvalid) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "写入配置失败: "+err.Error())
		return
	}
	if settings == nil {
		settings = []Setting{}
	}
	writeJSON(w, http.StatusOK, SettingsResponse{Settings: settings})
}
