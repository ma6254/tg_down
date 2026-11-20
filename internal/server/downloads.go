package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// 下载任务接口的错误分类：业务层用这些哨兵错误表达可归因的失败。
var (
	// ErrDownloadNotFound 表示任务不存在（404）。
	ErrDownloadNotFound = errors.New("下载任务不存在")
	// ErrDownloadConflict 表示任务状态不允许当前操作（409）。
	ErrDownloadConflict = errors.New("下载任务当前状态不允许该操作")
)

// 下载任务状态，与 database 包保持一致。
const (
	DownloadStatusPending = "pending"
	DownloadStatusRunning = "running"
	DownloadStatusPaused  = "paused"
	DownloadStatusDone    = "done"
	DownloadStatusFailed  = "failed"
)

// maxDownloadLinks 是一次创建任务允许的最大链接数。
const maxDownloadLinks = 200

// DownloadEvent 是 SSE 推送的一条事件：Type 写入 `event:` 行，Data 序列化为 `data:`。
type DownloadEvent struct {
	Type string
	Data any
}

// DownloadTask 是一条下载任务的状态快照。
type DownloadTask struct {
	ID        uint   `json:"id"`
	Link      string `json:"link"`
	AccountID int64  `json:"account_id"`
	ChatID    int64  `json:"chat_id"`
	ChatTitle string `json:"chat_title,omitempty"`
	MessageID int    `json:"message_id"`
	FileName  string `json:"file_name"`
	FileSize  int64  `json:"file_size"`
	Kind      string `json:"kind,omitempty"`
	MIME      string `json:"mime,omitempty"`

	Status   string `json:"status"` // pending | running | paused | done | failed
	Offset   int64  `json:"offset"` // 已下载字节数
	Speed    int64  `json:"speed"`  // B/s，仅 running
	Error    string `json:"error,omitempty"`
	SavePath string `json:"save_path,omitempty"` // 完成后才有值
	// Overwrite 表示该任务会覆盖同名文件。
	Overwrite bool `json:"overwrite,omitempty"`
	// ThumbnailURL 是任务卡片缩略图；没有可用封面时省略。
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	// PreviewURL 是较大尺寸的预览图，供图片查看器使用；没有时省略。
	PreviewURL string `json:"preview_url,omitempty"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// DownloadCreateRequest 是 POST /downloads 的请求体。
type DownloadCreateRequest struct {
	Links []string `json:"links"`
	// Overwrite 为 true 时，完成后覆盖同名文件，并丢弃已有 .part 从零下载；
	// 默认 false 时遇到同名文件会加 " (1)" 后缀。
	Overwrite bool `json:"overwrite,omitempty"`
	// Single 为 true 时只下载链接指向的那条消息，不展开相册；
	// 默认 false 与 CLI 一致：相册链接展开成整组任务。
	Single bool `json:"single,omitempty"`
}

// DownloadSkip 记录因「已存在」或「没有可下载媒体」跳过的链接。
type DownloadSkip struct {
	Link   string `json:"link"`
	Reason string `json:"reason"`
}

// DownloadFailure 记录解析失败的链接。
type DownloadFailure struct {
	Link  string `json:"link"`
	Error string `json:"error"`
}

// DownloadCreateResponse 是创建任务的结果。
type DownloadCreateResponse struct {
	Created  []uint            `json:"created"`           // 新建并已入队的任务
	Existing []uint            `json:"existing"`          // 之前已存在的任务
	Skipped  []DownloadSkip    `json:"skipped,omitempty"` // 没有可下载媒体 / 超过上限
	Failed   []DownloadFailure `json:"failed,omitempty"`  // 链接解析或查询失败
}

// DownloadListQuery 是列表接口的筛选条件。
type DownloadListQuery struct {
	Status string
	Limit  int
}

// downloadStatuses 是允许查询的状态集合。
var downloadStatuses = map[string]struct{}{
	DownloadStatusPending: {},
	DownloadStatusRunning: {},
	DownloadStatusPaused:  {},
	DownloadStatusDone:    {},
	DownloadStatusFailed:  {},
}

// handleDownloadCreate 处理 POST /api/v1/downloads：
// 解析链接、按消息创建下载任务并入队，重复链接复用已有任务。
func (s *Server) handleDownloadCreate(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CreateDownloads == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	var req DownloadCreateRequest
	if r.Body != nil {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, previewBodyLimit))
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法 JSON: "+err.Error())
			return
		}
	}

	req.Links = normalizePreviewLinks(req.Links)
	if len(req.Links) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "links 不能为空")
		return
	}
	if len(req.Links) > maxDownloadLinks {
		writeError(w, http.StatusBadRequest, "bad_request",
			fmt.Sprintf("一次最多创建 %d 条下载任务", maxDownloadLinks))
		return
	}

	resp, err := s.cfg.CreateDownloads(r.Context(), req)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	if resp.Created == nil {
		resp.Created = []uint{}
	}
	if resp.Existing == nil {
		resp.Existing = []uint{}
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleDownloadList 处理 GET /api/v1/downloads?status=&limit=。
func (s *Server) handleDownloadList(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ListDownloads == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	query := r.URL.Query()
	status := strings.TrimSpace(query.Get("status"))
	if status != "" {
		if _, ok := downloadStatuses[status]; !ok {
			writeError(w, http.StatusBadRequest, "bad_request",
				"status 只能是 pending、running、paused、done 或 failed")
			return
		}
	}
	limit := 100
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit 必须是 1-200 的整数")
			return
		}
		limit = value
	}

	tasks, err := s.cfg.ListDownloads(r.Context(), DownloadListQuery{Status: status, Limit: limit})
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	if tasks == nil {
		tasks = []DownloadTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// handleDownloadGet 处理 GET /api/v1/downloads/{id}。
func (s *Server) handleDownloadGet(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownload == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	id, ok := parseDownloadID(w, r)
	if !ok {
		return
	}
	task, err := s.cfg.GetDownload(r.Context(), id)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleDownloadControl 处理 POST /api/v1/downloads/{id}/{action}：
// action 取值 pause / resume / retry。
func (s *Server) handleDownloadControl(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ControlDownload == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	id, ok := parseDownloadID(w, r)
	if !ok {
		return
	}

	action := strings.TrimSpace(r.PathValue("action"))
	switch action {
	case "pause", "resume", "retry":
	default:
		writeError(w, http.StatusNotFound, "not_found", "接口不存在: "+r.URL.Path)
		return
	}

	task, err := s.cfg.ControlDownload(r.Context(), id, action)
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// handleDownloadDelete 处理 DELETE /api/v1/downloads/{id}?delete_file=true。
func (s *Server) handleDownloadDelete(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DeleteDownload == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	id, ok := parseDownloadID(w, r)
	if !ok {
		return
	}
	deleteFile := false
	if raw := strings.TrimSpace(r.URL.Query().Get("delete_file")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "delete_file 必须是 true 或 false")
			return
		}
		deleteFile = value
	}

	if err := s.cfg.DeleteDownload(r.Context(), id, deleteFile); err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// parseDownloadID 解析路径里的任务 ID；非法时写出 400 并返回 false。
func parseDownloadID(w http.ResponseWriter, r *http.Request) (uint, bool) {
	raw := strings.TrimSpace(r.PathValue("id"))
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "任务 ID 必须是正整数")
		return 0, false
	}
	return uint(value), true
}

// writeDownloadError 把业务错误映射成 HTTP 状态码。
func writeDownloadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDownloadNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrDownloadConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrNoCurrentAccount), errors.Is(err, ErrAccountNoSession):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "download_timeout", "解析链接超时，请稍后重试")
	case errors.Is(err, ErrPreviewFailed):
		writeError(w, http.StatusBadGateway, "download_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
