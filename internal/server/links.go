package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// 链接解析可以返回的业务错误，handler 据此选择状态码。
var (
	// ErrNoCurrentAccount 表示还没有选择当前账号。
	ErrNoCurrentAccount = errors.New("没有当前账号")
	// ErrPreviewFailed 表示联网解析失败（连接、鉴权、Telegram 报错等）。
	ErrPreviewFailed = errors.New("解析消息失败")
)

const (
	// maxPreviewLinks 限制一次解析的链接数量，避免单个请求拖太久。
	maxPreviewLinks = 200
	// previewBodyLimit 限制请求体大小：200 条链接远小于该值。
	previewBodyLimit = 1 << 20
)

// PreviewRequest 是 POST /api/v1/links/preview 的请求体，对应 CLI 的：
//
//	tg_down download -u <链接> [--expand-text-links]
type PreviewRequest struct {
	// Links 是待解析的 Telegram 消息链接，一行一条由调用方拆分。
	Links []string `json:"links"`
	// ExpandTextLinks 为 true 时，展开消息正文里的文字链接（只展开一层）。
	ExpandTextLinks bool `json:"expand_text_links,omitempty"`
}

// PreviewChat 是消息所在会话的摘要。
type PreviewChat struct {
	Kind     string `json:"kind"` // user | group | channel
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username,omitempty"`
	// Broadcast 表示广播频道（kind=channel 时用来区分频道与超级群）。
	Broadcast bool `json:"broadcast,omitempty"`
	// Self 表示用户会话就是当前账号，即收藏夹（Saved Messages）。
	Self bool `json:"self,omitempty"`
	// AvatarURL 是群聊头像（token 地址，会过期）；私聊/频道通常省略。
	AvatarURL string `json:"avatar_url,omitempty"`
}

// PreviewSender 是群聊消息的发送者。
type PreviewSender struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// PreviewLink 是消息正文里的一段文字链接（text_link / url 实体）。
type PreviewLink struct {
	Text string `json:"text,omitempty"`
	URL  string `json:"url"`
}

// PreviewEntity 是消息正文的富文本标注，字段含义与 Bot API 的 MessageEntity 一致。
//
// Offset/Length 以 UTF-16 码元计；只有对应类型才会带 URL、UserID 等附加字段。
type PreviewEntity struct {
	Type       string `json:"type"`
	Offset     int    `json:"offset"`
	Length     int    `json:"length"`
	URL        string `json:"url,omitempty"`         // text_link
	UserID     int64  `json:"user_id,omitempty"`     // text_mention
	Language   string `json:"language,omitempty"`    // pre
	DocumentID int64  `json:"document_id,omitempty"` // custom_emoji
	Collapsed  bool   `json:"collapsed,omitempty"`   // blockquote（可折叠）
	Date       int    `json:"date,omitempty"`        // date_time
	DateFormat string `json:"date_format,omitempty"` // date_time
}

// PreviewMedia 是消息里的可下载媒体。
type PreviewMedia struct {
	Kind     string `json:"kind"` // photo | video | audio | document
	FileName string `json:"file_name"`
	Size     int64  `json:"size"`
	// MessageID 是这条媒体所在的消息；相册里每张图对应不同消息。
	MessageID int `json:"message_id,omitempty"`
	// Link 是这条消息的链接（相册成员可单独入队）；无法拼出时省略。
	Link         string `json:"link,omitempty"`
	MIME         string `json:"mime,omitempty"`
	Duration     int    `json:"duration,omitempty"` // 秒
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	GroupedID    int64  `json:"grouped_id,omitempty"` // 相册分组
	Downloadable bool   `json:"downloadable"`
	MaxSize      int64  `json:"max_size"` // 当前账号的单文件上限
	// ThumbnailURL 是列表封面地址；没有可用缩略图时省略。
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	// PreviewURL 是正文大图地址（比封面更大）；没有时前端退回 ThumbnailURL。
	PreviewURL string `json:"preview_url,omitempty"`
}

// PreviewItem 是一条链接的解析结果；Error 非空时其它字段无意义。
type PreviewItem struct {
	Link      string       `json:"link"`
	Expanded  bool         `json:"expanded,omitempty"` // 来自正文链接展开
	MessageID int          `json:"message_id,omitempty"`
	Chat      *PreviewChat `json:"chat,omitempty"`
	// Sender 是群聊消息的发送者；私聊/频道为空。
	Sender *PreviewSender `json:"sender,omitempty"`
	Text   string         `json:"text,omitempty"`
	Links  []PreviewLink  `json:"links,omitempty"`
	// Entities 是正文的富文本标注；前端据此渲染粗体、代码、引用等格式。
	Entities []PreviewEntity `json:"entities,omitempty"`
	Media    *PreviewMedia   `json:"media,omitempty"` // 纯文本消息为空
	// Medias 是相册的全部媒体（按消息 ID 升序）；非相册消息为空，只用 Media。
	Medias []PreviewMedia `json:"medias,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// PreviewSummary 汇总一次解析的结果。
type PreviewSummary struct {
	Total        int  `json:"total"`
	OK           int  `json:"ok"`
	Failed       int  `json:"failed"`
	Expanded     int  `json:"expanded"`
	NoMedia      int  `json:"no_media"`
	SkippedLinks int  `json:"skipped_links,omitempty"` // 正文里不是消息链接的条数
	Truncated    bool `json:"truncated,omitempty"`     // 展开数量超过上限被截断
}

// PreviewResponse 是 POST /api/v1/links/preview 的响应。
type PreviewResponse struct {
	Items   []PreviewItem  `json:"items"`
	Summary PreviewSummary `json:"summary"`
}

// handlePreviewLinks 处理 POST /api/v1/links/preview：
// 解析 Telegram 消息链接，返回正文、内嵌链接与媒体元数据，不写数据库。
func (s *Server) handlePreviewLinks(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PreviewLinks == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "解析链接功能未接入")
		return
	}

	var req PreviewRequest
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
	if len(req.Links) > maxPreviewLinks {
		writeError(w, http.StatusBadRequest, "bad_request",
			fmt.Sprintf("一次最多解析 %d 条链接", maxPreviewLinks))
		return
	}

	resp, err := s.cfg.PreviewLinks(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrNoCurrentAccount), errors.Is(err, ErrAccountNoSession):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "preview_timeout",
				"解析消息超时：请减少链接数量或稍后重试")
		case errors.Is(err, ErrPreviewFailed):
			writeError(w, http.StatusBadGateway, "preview_failed", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "解析消息失败: "+err.Error())
		}
		return
	}

	if resp.Items == nil {
		resp.Items = []PreviewItem{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// normalizePreviewLinks 去除首尾空白与空行，保持原有顺序。
func normalizePreviewLinks(links []string) []string {
	out := make([]string, 0, len(links))
	for _, link := range links {
		if trimmed := strings.TrimSpace(link); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
