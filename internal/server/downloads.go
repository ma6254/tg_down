package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 下载任务接口的错误分类：业务层用这些哨兵错误表达可归因的失败。
var (
	// ErrDownloadNotFound 表示任务不存在（404）。
	ErrDownloadNotFound = errors.New("下载任务不存在")
	// ErrDownloadConflict 表示任务状态不允许当前操作（409）。
	ErrDownloadConflict = errors.New("下载任务当前状态不允许该操作")
	// ErrDownloadFileNotFound 表示任务存在，但已下载文件不存在或已被删除（404）。
	ErrDownloadFileNotFound = errors.New("已下载文件不存在或已被删除")
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

// DownloadStats 是下载队列的总览快照。
type DownloadStats struct {
	// TotalSpeed 是所有运行中任务的合计速度，单位 B/s。
	TotalSpeed int64 `json:"total_speed"`
	// RunningTasks 是当前运行中的任务数。
	RunningTasks int `json:"running_tasks"`
}

// DownloadSpeedPoint 是历史速度曲线上的一个聚合点。
type DownloadSpeedPoint struct {
	// At 是桶起始时间，Unix 秒。
	At int64 `json:"at"`
	// TotalSpeed 是桶内平均合计速度，B/s。
	TotalSpeed int64 `json:"total_speed"`
	// RunningTasks 是桶内最大运行任务数。
	RunningTasks int `json:"running_tasks"`
}

// DownloadSpeedHistory 是一段历史速度。
type DownloadSpeedHistory struct {
	From           int64                `json:"from"`
	To             int64                `json:"to"`
	Bucket         int                  `json:"bucket"`
	SampleInterval int                  `json:"sample_interval"`
	StatsSince     int64                `json:"stats_since"`
	AverageSpeed   int64                `json:"average_speed"`
	PeakSpeed      int64                `json:"peak_speed"`
	Points         []DownloadSpeedPoint `json:"points"`
}

// DownloadSpeedStatsReset 是清空平均/峰值统计后的响应。
type DownloadSpeedStatsReset struct {
	OK      bool  `json:"ok"`
	ResetAt int64 `json:"reset_at"`
}

// DownloadSpeedHistoryQuery 是历史速度接口的查询条件。
type DownloadSpeedHistoryQuery struct {
	From   time.Time
	To     time.Time
	Bucket time.Duration // 0 表示自动选择
}

// 历史速度接口的默认值与上限。
const (
	defaultDownloadSpeedHistoryWindow = 30 * time.Minute
	maxDownloadSpeedHistoryRange      = 24 * time.Hour
	minDownloadSpeedHistoryBucket     = 1
	maxDownloadSpeedHistoryBucket     = 3600
	maxDownloadSpeedHistoryPoints     = 1000
)

// DownloadCreateRequest 是 POST /downloads 的请求体。
type DownloadCreateRequest struct {
	Links []string `json:"links"`
	// Overwrite 为 true 时，完成后覆盖同名文件，并丢弃已有 .part 从零下载；
	// 默认 false 时遇到同名文件会加 " (1)" 后缀。
	Overwrite bool `json:"overwrite,omitempty"`
	// Single 为 true 时只下载链接指向的那条消息，不展开相册；
	// 默认 false 与 CLI 一致：相册链接展开成整组任务。
	Single bool `json:"single,omitempty"`
	// AllowOverLimit 为 true 时跳过本地的单文件上限预检，强制创建任务；
	// Telegram 服务端仍可能拒绝，失败会体现在任务 error。
	AllowOverLimit bool `json:"allow_over_limit,omitempty"`
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
	Created []uint `json:"created"` // 新建并已入队的任务
	// Restarted 是覆盖模式下被重置并重新入队的已有任务。
	Restarted []uint            `json:"restarted,omitempty"`
	Existing  []uint            `json:"existing"`          // 之前已存在的任务
	Skipped   []DownloadSkip    `json:"skipped,omitempty"` // 没有可下载媒体 / 超过上限
	Failed    []DownloadFailure `json:"failed,omitempty"`  // 链接解析或查询失败
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

// handleDownloadStats 处理 GET /api/v1/downloads/stats：运行中任务的合计速度。
//
// 只读内存里的实时进度，不查数据库；没有运行中的任务时返回 0，
// 因此不会因为「当前没有下载」而报 404。
func (s *Server) handleDownloadStats(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadStats == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}
	writeJSON(w, http.StatusOK, s.cfg.GetDownloadStats(r.Context()))
}

// handleDownloadSpeedHistory 处理 GET /api/v1/downloads/stats/history。
//
// 参数：from / to 为 Unix 秒（默认最近 30 分钟），bucket 为聚合粒度（秒，省略则自动）。
// 数据来自内存环形缓冲，进程重启后清空。
func (s *Server) handleDownloadSpeedHistory(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetDownloadSpeedHistory == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}

	now := time.Now()
	from := now.Add(-defaultDownloadSpeedHistoryWindow)
	to := now
	query := r.URL.Query()
	fromSet, toSet := false, false

	if raw := strings.TrimSpace(query.Get("from")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "from 必须是 Unix 秒时间戳")
			return
		}
		from = time.Unix(value, 0)
		fromSet = true
	}
	if raw := strings.TrimSpace(query.Get("to")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "to 必须是 Unix 秒时间戳")
			return
		}
		to = time.Unix(value, 0)
		toSet = true
	}
	// 只给 to 时，默认看它之前的 30 分钟；未来时间收敛到当前时刻。
	if !fromSet && toSet {
		from = to.Add(-defaultDownloadSpeedHistoryWindow)
	}
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, "bad_request", "from 必须早于 to")
		return
	}
	if to.Sub(from) > maxDownloadSpeedHistoryRange {
		writeError(w, http.StatusBadRequest, "bad_request", "查询范围不能超过 24 小时")
		return
	}

	var bucket time.Duration
	if raw := strings.TrimSpace(query.Get("bucket")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < minDownloadSpeedHistoryBucket || value > maxDownloadSpeedHistoryBucket {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("bucket 必须是 %d-%d 的整数（秒）",
					minDownloadSpeedHistoryBucket, maxDownloadSpeedHistoryBucket))
			return
		}
		bucket = time.Duration(value) * time.Second
		if int(to.Sub(from)/bucket)+1 > maxDownloadSpeedHistoryPoints {
			writeError(w, http.StatusBadRequest, "bad_request",
				fmt.Sprintf("bucket 太小：最多返回 %d 个点，请增大 bucket 或缩小范围",
					maxDownloadSpeedHistoryPoints))
			return
		}
	}

	history, err := s.cfg.GetDownloadSpeedHistory(r.Context(), DownloadSpeedHistoryQuery{
		From:   from,
		To:     to,
		Bucket: bucket,
	})
	if err != nil {
		writeDownloadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

// handleDownloadSpeedStatsReset 处理 POST /api/v1/downloads/stats/reset：
// 只重置平均/峰值统计的起点，速度曲线样本保留。
func (s *Server) handleDownloadSpeedStatsReset(w http.ResponseWriter, r *http.Request) {
	if s.cfg.ResetDownloadSpeedStats == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "下载功能未接入")
		return
	}
	resetAt := s.cfg.ResetDownloadSpeedStats(r.Context())
	writeJSON(w, http.StatusOK, DownloadSpeedStatsReset{
		OK:      true,
		ResetAt: resetAt.Unix(),
	})
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
	case errors.Is(err, ErrDownloadNotFound), errors.Is(err, ErrDownloadFileNotFound):
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
