package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ma6254/tg_down/internal/downloadinput"
)

// 下载页草稿与历史的可归因错误，handler 据此选择状态码。
var (
	// ErrDownloadDraftInvalid 表示草稿超长/超行数等（400）。
	ErrDownloadDraftInvalid = errors.New("下载页草稿不合法")
	// ErrDownloadHistoryNotFound 表示历史记录不存在（404）。
	ErrDownloadHistoryNotFound = errors.New("下载历史记录不存在")
	// ErrDownloadHistoryInvalid 表示历史输入超长/超行数等（400）。
	ErrDownloadHistoryInvalid = errors.New("下载历史记录不合法")
)

// downloadInputBodyLimit 是草稿/历史请求体上限：links 256 KiB + cache 32 KiB
// 再留 JSON 包装余量；明显超过时直接 400，而不是先读进内存。
const downloadInputBodyLimit = downloadinput.MaxLinksBytes + downloadinput.MaxCacheBytes + 64<<10

// handleDownloadDraftGet 处理 GET /api/v1/downloads/draft：
// 没有草稿时返回默认值（200），前端不用区分 404。
func (s *Server) handleDownloadDraftGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadDraft == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载草稿功能未接入")
		return
	}
	draft, err := s.cfg.GetDownloadDraft(r.Context())
	if err != nil {
		writeDownloadDraftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// handleDownloadDraftPut 处理 PUT /api/v1/downloads/draft：全量替换输入。
func (s *Server) handleDownloadDraftPut(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SaveDownloadDraft == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载草稿功能未接入")
		return
	}

	var req DownloadDraftInput
	if !decodeDownloadInputBody(w, r, &req) {
		return
	}

	draft, err := s.cfg.SaveDownloadDraft(r.Context(), req)
	if err != nil {
		writeDownloadDraftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// handleDownloadHistoryList 处理 GET /api/v1/downloads/history?limit=20。
func (s *Server) handleDownloadHistoryList(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ListDownloadHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史功能未接入")
		return
	}

	limit := downloadinput.HistoryLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > downloadinput.HistoryLimit {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("limit 必须是 1-%d 的整数", downloadinput.HistoryLimit))
			return
		}
		limit = value
	}

	list, err := s.cfg.ListDownloadHistory(r.Context(), limit)
	if err != nil {
		writeDownloadHistoryError(w, err)
		return
	}
	if list.Items == nil {
		list.Items = []DownloadHistoryEntry{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleDownloadHistoryGet 处理 GET /api/v1/downloads/history/{id}。
func (s *Server) handleDownloadHistoryGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史功能未接入")
		return
	}

	id, ok := parseDownloadHistoryID(w, r)
	if !ok {
		return
	}
	entry, err := s.cfg.GetDownloadHistory(r.Context(), id)
	if err != nil {
		writeDownloadHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleDownloadHistoryCover 处理 GET /api/v1/downloads/history/{id}/cover：
// 从缩略图磁盘缓存返回该条历史第一张封面的 JPEG 字节；没有封面时 404。
func (s *Server) handleDownloadHistoryCover(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadHistoryCover == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史封面未接入")
		return
	}

	id, ok := parseDownloadHistoryID(w, r)
	if !ok {
		return
	}
	thumbnail, err := s.cfg.GetDownloadHistoryCover(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, ErrDownloadHistoryNotFound), errors.Is(err, ErrThumbnailNotFound):
			writeError(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "thumbnail_timeout", "封面读取超时，请稍后重试")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}

	contentType := thumbnail.ContentType
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	// 缓存键由媒体身份决定，同一张封面内容不会变化。
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(thumbnail.Data)
}

// handleDownloadHistoryCreate 处理 POST /api/v1/downloads/history：
// 新建返回 201，内容重复时更新并返回 200。
func (s *Server) handleDownloadHistoryCreate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.RecordDownloadHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史功能未接入")
		return
	}

	var req DownloadHistoryInput
	if !decodeDownloadInputBody(w, r, &req) {
		return
	}

	entry, created, err := s.cfg.RecordDownloadHistory(r.Context(), req)
	if err != nil {
		writeDownloadHistoryError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, entry)
}

// handleDownloadHistoryDelete 处理 DELETE /api/v1/downloads/history/{id}。
func (s *Server) handleDownloadHistoryDelete(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DeleteDownloadHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史功能未接入")
		return
	}

	id, ok := parseDownloadHistoryID(w, r)
	if !ok {
		return
	}
	if err := s.cfg.DeleteDownloadHistory(r.Context(), id); err != nil {
		writeDownloadHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDownloadHistoryClear 处理 DELETE /api/v1/downloads/history：清空全部历史。
func (s *Server) handleDownloadHistoryClear(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ClearDownloadHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载历史功能未接入")
		return
	}

	removed, err := s.cfg.ClearDownloadHistory(r.Context())
	if err != nil {
		writeDownloadHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": removed})
}

// decodeDownloadInputBody 解码草稿/历史请求体，失败时写出 400 并返回 false。
func decodeDownloadInputBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不能为空")
		return false
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, downloadInputBodyLimit))
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("请求体超过 %d KiB 上限", downloadInputBodyLimit>>10))
			return false
		}
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
		return false
	}
	return true
}

// parseDownloadHistoryID 解析路径里的历史 ID；非法时写出 400 并返回 false。
func parseDownloadHistoryID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	raw := strings.TrimSpace(r.PathValue("id"))
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "历史记录 ID 必须是正整数")
		return 0, false
	}
	return uint(value), true
}

// writeDownloadDraftError 把草稿业务错误映射成 HTTP 状态码。
func writeDownloadDraftError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDownloadDraftInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

// writeDownloadHistoryError 把历史业务错误映射成 HTTP 状态码。
func writeDownloadHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDownloadHistoryNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrDownloadHistoryInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
