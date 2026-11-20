package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ma6254/tg_down/internal/config"
)

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

// readOnlySettings 是不能通过配置接口直接写的键，由专门的接口维护。
var readOnlySettings = map[string]bool{
	config.KeyCurrentAccountID: true,
}

// newSetting 把配置记录转换成接口结构。
//
// 这是本包唯一的「领域 → API」映射入口：cmd 层只负责把库里的记录读出来，
// 展示用的脱敏值、说明与只读标记都在这里补齐。
func newSetting(entry config.Entry) Setting {
	return Setting{
		Key:         entry.Key,
		Type:        entry.Type,
		Default:     entry.Default,
		Value:       config.Mask(entry.Key, entry.Value),
		Description: entry.Description,
		Known:       entry.Known,
		ReadOnly:    readOnlySettings[entry.Key],
	}
}

// newSettings 批量转换；空列表返回空切片而不是 nil，前端按数组遍历。
func newSettings(entries []config.Entry) []Setting {
	out := make([]Setting, 0, len(entries))
	for _, entry := range entries {
		out = append(out, newSetting(entry))
	}
	return out
}

// handleSettingsGet 处理 GET /api/v1/settings：返回全部配置项。
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetSettings == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "配置读取功能未接入")
		return
	}

	entries, err := s.cfg.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "读取配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SettingsResponse{Settings: newSettings(entries)})
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

	// 只读项由专门接口维护：先拦下来，避免业务层写进去。
	for key := range patch {
		if readOnlySettings[key] {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf(
				"%s 由账号接口维护，请用 POST /api/v1/accounts/{id}/select", key))
			return
		}
	}

	entries, err := s.cfg.UpdateSettings(r.Context(), map[string]*string(patch))
	if err != nil {
		if errors.Is(err, config.ErrInvalidSetting) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "写入配置失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SettingsResponse{Settings: newSettings(entries)})
}
