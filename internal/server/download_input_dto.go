package server

import (
	"encoding/json"
	"time"

	"github.com/ma6254/tg_down/internal/downloadinput"
)

// DownloadDraft 是 GET/PUT /downloads/draft 的响应。
//
// 两个开关都在系统配置里（download_overwrite / download_expand_text_links），
// 不属于草稿。
type DownloadDraft struct {
	Links     string               `json:"links"`
	Cache     *downloadinput.Cache `json:"cache,omitempty"`
	UpdatedAt int64                `json:"updated_at,omitempty"`
}

// DownloadDraftInput 是 PUT /downloads/draft 的请求体。
type DownloadDraftInput struct {
	Links string          `json:"links"`
	Cache json.RawMessage `json:"cache,omitempty"`
}

// DownloadHistoryEntry 是历史列表里的一行。
//
// 列表不返回完整 links 与缓存条目，只给消息摘要、第一行、计数与摘要缓存，
// 回填时再取详情。
type DownloadHistoryEntry struct {
	ID        uint   `json:"id"`
	FirstLine string `json:"first_line"`
	// Title 是历史卡片的标题（消息摘要：正文/说明，或 [图片] 之类的媒体占位符）。
	Title           string                      `json:"title,omitempty"`
	LineCount       int                         `json:"line_count"`
	ExpandTextLinks bool                        `json:"expand_text_links"`
	Overwrite       bool                        `json:"overwrite"`
	UseCount        int                         `json:"use_count"`
	Summary         *downloadinput.CacheSummary `json:"summary,omitempty"`
	Chats           []downloadinput.CacheChat   `json:"chats,omitempty"`
	CacheTruncated  bool                        `json:"cache_truncated"`
	CreatedAt       int64                       `json:"created_at"`
	LastUsedAt      int64                       `json:"last_used_at"`
}

// DownloadHistoryDetail 是单条历史的详情：列表字段 + 完整 links 与缓存。
type DownloadHistoryDetail struct {
	DownloadHistoryEntry
	Links string               `json:"links"`
	Cache *downloadinput.Cache `json:"cache,omitempty"`
}

// DownloadHistoryList 是 GET /downloads/history 的响应。
type DownloadHistoryList struct {
	Items []DownloadHistoryEntry `json:"items"`
}

// DownloadHistoryInput 是 POST /downloads/history 的请求体。
type DownloadHistoryInput struct {
	Links           string          `json:"links"`
	ExpandTextLinks bool            `json:"expand_text_links"`
	Overwrite       bool            `json:"overwrite"`
	Cache           json.RawMessage `json:"cache,omitempty"`
}

// NewDownloadDraft 把领域模型转换成接口结构。
func NewDownloadDraft(draft downloadinput.Draft) DownloadDraft {
	return DownloadDraft{
		Links:     draft.Links,
		Cache:     draft.Cache,
		UpdatedAt: unixOrZero(draft.UpdatedAt),
	}
}

// NewDownloadHistoryEntry 转换历史列表的一行。
func NewDownloadHistoryEntry(entry downloadinput.Entry) DownloadHistoryEntry {
	out := DownloadHistoryEntry{
		ID:              entry.ID,
		FirstLine:       entry.FirstLine,
		Title:           entry.Title,
		LineCount:       entry.LineCount,
		ExpandTextLinks: entry.ExpandTextLinks,
		Overwrite:       entry.Overwrite,
		UseCount:        entry.UseCount,
		CacheTruncated:  entry.CacheTruncated,
		CreatedAt:       unixOrZero(entry.CreatedAt),
		LastUsedAt:      unixOrZero(entry.LastUsedAt),
	}
	if entry.Cache != nil {
		summary := entry.Cache.Summary
		out.Summary = &summary
		out.Chats = entry.Cache.Chats
	}
	return out
}

// NewDownloadHistoryDetail 转换单条历史详情。
func NewDownloadHistoryDetail(entry downloadinput.Entry) DownloadHistoryDetail {
	return DownloadHistoryDetail{
		DownloadHistoryEntry: NewDownloadHistoryEntry(entry),
		Links:                entry.Links,
		Cache:                entry.Cache,
	}
}

// NewDownloadHistoryList 转换历史列表，保证空结果是 [] 而不是 null。
func NewDownloadHistoryList(entries []downloadinput.Entry) DownloadHistoryList {
	items := make([]DownloadHistoryEntry, 0, len(entries))
	for _, entry := range entries {
		items = append(items, NewDownloadHistoryEntry(entry))
	}
	return DownloadHistoryList{Items: items}
}

// unixOrZero 把零值时间转成 0，避免响应里出现负数时间戳。
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
