// Package downloadinput 管理下载页的输入草稿与历史记录。
//
// 草稿只有一份（ui_drafts.key = downloads），保存用户正在编辑的输入与上次解析摘要；
// 历史按「归一后的 links + 展开开关」去重（覆盖开关不参与），保留最近 20 条，供下载页回填。
// 解析摘要按白名单裁剪后落库，只用于展示，不参与创建下载任务。
package downloadinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ma6254/tg_down/internal/thumbcache"
)

const (
	// DraftKey 是下载页草稿在 ui_drafts 里的主键。
	DraftKey = "downloads"
	// MaxLinksBytes 是 links 文本的字节上限（256 KiB）。
	MaxLinksBytes = 256 << 10
	// MaxLines 是 links 的非空行数上限。
	MaxLines = 500
	// MaxCacheBytes 是序列化后缓存 JSON 的字节上限（32 KiB）。
	MaxCacheBytes = 32 << 10
	// CacheVersion 是缓存结构版本。
	CacheVersion = 1
	// HistoryLimit 是历史记录保留/返回条数上限。
	HistoryLimit = 20

	// 缓存内各列表与文本的裁剪上限。
	maxCacheItems    = 20
	maxCacheChats    = 5
	maxCacheFailures = 5
	maxCacheText     = 200
	maxCacheTitle    = 200
	maxCacheUsername = 64
	maxCacheFileName = 255
	// maxSummaryTitle 是历史卡片标题的长度上限（卡片单行展示，后端先截断）。
	maxSummaryTitle = 120
)

// ErrInvalid 表示草稿或历史输入不合法（超长、超行数等），对应 HTTP 400。
var ErrInvalid = errors.New("下载页输入不合法")

// Cache 是解析摘要缓存，字段白名单固定在这里。
//
// 结构与 doc/api.md §1.8 一致；items/chats/failures 都是裁剪后的展示数据，
// 不含缩略图地址、entities、file_id 等会过期或敏感的字段。
type Cache struct {
	V        int            `json:"v"`
	Summary  CacheSummary   `json:"summary"`
	Chats    []CacheChat    `json:"chats,omitempty"`
	Items    []CacheItem    `json:"items,omitempty"`
	Failures []CacheFailure `json:"failures,omitempty"`
}

// CacheSummary 是预览结果的计数摘要。
type CacheSummary struct {
	Total    int `json:"total"`
	OK       int `json:"ok"`
	Failed   int `json:"failed"`
	NoMedia  int `json:"no_media"`
	Expanded int `json:"expanded"`
}

// CacheChat 是解析结果里出现过的会话。
type CacheChat struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Username string `json:"username,omitempty"`
}

// CacheItem 是一条消息的展示摘要（正文前 200 字与主媒体信息）。
type CacheItem struct {
	MessageID int    `json:"message_id,omitempty"`
	ChatTitle string `json:"chat_title,omitempty"`
	Text      string `json:"text,omitempty"`
	MediaKind string `json:"media_kind,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	Size      int64  `json:"size,omitempty"`
	// ThumbKey 是缩略图的磁盘缓存键，供历史卡片取封面；必须是 64 位十六进制。
	ThumbKey string `json:"thumb_key,omitempty"`
}

// CacheFailure 是解析失败的一条链接与原因。
type CacheFailure struct {
	Link  string `json:"link"`
	Error string `json:"error"`
}

// Draft 是下载页草稿的领域模型。
//
// 「覆盖同名文件」与「展开正文里的链接」已迁移到系统配置
// （settings.download_overwrite / download_expand_text_links），不在这里保存。
type Draft struct {
	Links     string
	Cache     *Cache
	UpdatedAt time.Time
}

// Entry 是一条下载历史记录。
type Entry struct {
	ID              uint
	Links           string
	LineCount       int
	FirstLine       string
	Title           string
	ExpandTextLinks bool
	Overwrite       bool
	UseCount        int
	Cache           *Cache
	CacheTruncated  bool
	CreatedAt       time.Time
	LastUsedAt      time.Time
}

// SaveDraftRequest 是全量保存草稿的输入。
type SaveDraftRequest struct {
	Links string
	// Cache 为 nil 表示请求里没带 cache，保留已有缓存；
	// json.RawMessage("null") 表示显式清除。
	Cache json.RawMessage
}

// RecordRequest 是记录一次成功解析的输入。
type RecordRequest struct {
	Links           string
	ExpandTextLinks bool
	Overwrite       bool
	// Cache 省略、null 或非法时不保存摘要，仍记录输入。
	Cache json.RawMessage
}

// storedDraft 是 ui_drafts.value 的 JSON 结构。
type storedDraft struct {
	Links string          `json:"links"`
	Cache json.RawMessage `json:"cache,omitempty"`
}

// ValidateLinks 校验 links 的字节数与行数；空文本合法（表示主动清空）。
func ValidateLinks(links string) error {
	_, _, _, err := inspectLinks(links)
	return err
}

// inspectLinks 返回归一后的链接文本、非空行数与第一行（截断 160 字符）。
//
// 归一规则：统一换行、逐行 trim、去掉空行；历史去重与行数统计都用它。
func inspectLinks(links string) (normalized string, lineCount int, firstLine string, err error) {
	if len(links) > MaxLinksBytes {
		return "", 0, "", invalidf("links 超过 %d KiB 上限", MaxLinksBytes>>10)
	}

	lines := strings.Split(strings.ReplaceAll(links, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lineCount++
		if lineCount > MaxLines {
			return "", 0, "", invalidf("links 超过 %d 行上限", MaxLines)
		}
		if firstLine == "" {
			firstLine = truncateRunes(trimmed, 160)
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n"), lineCount, firstLine, nil
}

// Hash 计算历史记录的去重键：归一后的 links。
//
// 两个开关（展开正文链接、覆盖同名文件）都是全局配置，不参与去重：
// 同一批链接只切换开关时仍算同一条历史，重复记录会更新摘要与开关快照。
func Hash(normalizedLinks string) string {
	sum := sha256.Sum256([]byte(normalizedLinks))
	return hex.EncodeToString(sum[:])
}

// ParseCache 解析并裁剪前端送来的解析摘要。
//
// 未知字段被丢弃，超限列表截断，文本按上限裁剪；整体超过 32 KiB 时
// 继续丢弃列表项，仍然放不下则返回错误（调用方丢弃缓存、保留输入）。
func ParseCache(raw []byte) (*Cache, error) {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return nil, nil
	}

	var cache Cache
	if err := json.Unmarshal(raw, &cache); err != nil {
		return nil, invalidf("cache 不是合法的解析摘要")
	}
	return normalizeCache(&cache)
}

// normalizeCache 按白名单裁剪缓存并检查序列化大小。
func normalizeCache(cache *Cache) (*Cache, error) {
	cache.V = CacheVersion
	cache.Summary = clampSummary(cache.Summary)

	if len(cache.Chats) > maxCacheChats {
		cache.Chats = cache.Chats[:maxCacheChats]
	}
	chats := make([]CacheChat, 0, len(cache.Chats))
	for _, chat := range cache.Chats {
		kind := strings.TrimSpace(chat.Kind)
		switch kind {
		case "user", "group", "channel":
		default:
			kind = ""
		}
		chats = append(chats, CacheChat{
			Kind:     kind,
			Title:    truncateRunes(strings.TrimSpace(chat.Title), maxCacheTitle),
			Username: truncateRunes(strings.TrimSpace(chat.Username), maxCacheUsername),
		})
	}
	cache.Chats = chats

	if len(cache.Items) > maxCacheItems {
		cache.Items = cache.Items[:maxCacheItems]
	}
	items := make([]CacheItem, 0, len(cache.Items))
	for _, item := range cache.Items {
		size := item.Size
		if size < 0 {
			size = 0
		}
		items = append(items, CacheItem{
			MessageID: item.MessageID,
			ChatTitle: truncateRunes(strings.TrimSpace(item.ChatTitle), maxCacheTitle),
			Text:      truncateRunes(item.Text, maxCacheText),
			MediaKind: normalizeMediaKind(item.MediaKind),
			FileName:  truncateRunes(strings.TrimSpace(item.FileName), maxCacheFileName),
			Size:      size,
			ThumbKey:  normalizeThumbKey(item.ThumbKey),
		})
	}
	cache.Items = items

	if len(cache.Failures) > maxCacheFailures {
		cache.Failures = cache.Failures[:maxCacheFailures]
	}
	failures := make([]CacheFailure, 0, len(cache.Failures))
	for _, failure := range cache.Failures {
		failures = append(failures, CacheFailure{
			Link:  truncateRunes(strings.TrimSpace(failure.Link), maxCacheText),
			Error: truncateRunes(strings.TrimSpace(failure.Error), maxCacheText),
		})
	}
	cache.Failures = failures

	// 上面的裁剪已覆盖极端情况；这里仍按实际字节数兜底，保证落库不超过 32 KiB。
	for {
		data, err := json.Marshal(cache)
		if err != nil {
			return nil, invalidf("cache 无法序列化")
		}
		if len(data) <= MaxCacheBytes {
			return cache, nil
		}
		switch {
		case len(cache.Items) > 0:
			cache.Items = cache.Items[:len(cache.Items)-1]
		case len(cache.Failures) > 0:
			cache.Failures = cache.Failures[:len(cache.Failures)-1]
		case len(cache.Chats) > 0:
			cache.Chats = cache.Chats[:len(cache.Chats)-1]
		default:
			return nil, invalidf("cache 超过 %d KiB 上限", MaxCacheBytes>>10)
		}
	}
}

// clampSummary 把计数里的负数收敛为 0。
func clampSummary(summary CacheSummary) CacheSummary {
	clamp := func(value int) int {
		if value < 0 {
			return 0
		}
		return value
	}
	return CacheSummary{
		Total:    clamp(summary.Total),
		OK:       clamp(summary.OK),
		Failed:   clamp(summary.Failed),
		NoMedia:  clamp(summary.NoMedia),
		Expanded: clamp(summary.Expanded),
	}
}

// normalizeMediaKind 只接受接口约定的四种媒体类型。
func normalizeMediaKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "photo", "video", "audio", "document":
		return strings.TrimSpace(kind)
	default:
		return ""
	}
}

// normalizeThumbKey 只保留合法的缩略图缓存键，挡住路径穿越等异常输入。
func normalizeThumbKey(value string) string {
	key := strings.TrimSpace(value)
	if thumbcache.ValidKey(key) {
		return key
	}
	return ""
}

// truncateRunes 按字符截断文本，避免把一个多字节字符切成两半。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// isJSONNull 判断原始 JSON 是否就是 null。
func isJSONNull(raw []byte) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// SummaryTitle 生成历史卡片标题：优先正文/说明，其次媒体类型占位符。
//
// 标题取自缓存里的第一条消息；第一条没有可用内容时再按顺序找。
// 返回空串表示没有可用摘要，由前端退回第一行链接。
func SummaryTitle(cache *Cache) string {
	if cache == nil || len(cache.Items) == 0 {
		return ""
	}
	if title := itemTitle(cache.Items[0]); title != "" {
		return title
	}
	for _, item := range cache.Items[1:] {
		if title := itemTitle(item); title != "" {
			return title
		}
	}
	return ""
}

// itemTitle 把一条缓存消息压成单行标题。
func itemTitle(item CacheItem) string {
	if text := collapseWhitespace(item.Text); text != "" {
		return truncateRunes(text, maxSummaryTitle)
	}
	return mediaPlaceholder(item.MediaKind)
}

// collapseWhitespace 把换行与连续空白压成单个空格，适合单行标题。
func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// mediaPlaceholder 返回纯媒体消息的占位文案。
func mediaPlaceholder(kind string) string {
	switch kind {
	case "photo":
		return "[图片]"
	case "video":
		return "[视频]"
	case "audio":
		return "[音频]"
	case "document":
		return "[文件]"
	default:
		return ""
	}
}

// invalidf 生成带具体原因的 ErrInvalid。
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
