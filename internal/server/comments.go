package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

var (
	// ErrCommentsBadRequest 表示请求参数（链接/分页）不合法。
	ErrCommentsBadRequest = errors.New("评论请求不合法")
	// ErrCommentsFailed 表示联网读取评论区失败。
	ErrCommentsFailed = errors.New("读取评论区失败")
)

const (
	defaultCommentsLimit = mtproto.DefaultCommentsLimit
	maxCommentsLimit     = mtproto.MaxCommentsLimit
)

// CommentsQuery 是 GET /api/v1/links/comments 的查询参数。
type CommentsQuery struct {
	Link     string
	Limit    int
	OffsetID int
}

// CommentReply 是评论引用的回复目标。
type CommentReply struct {
	MessageID int    `json:"message_id"`
	Quote     string `json:"quote,omitempty"`
}

// Comment 是评论区里的一条评论。
type Comment struct {
	ID     int            `json:"id"`
	Date   int64          `json:"date"` // Unix 秒
	Sender *PreviewSender `json:"sender,omitempty"`
	Text   string         `json:"text,omitempty"`
	Links  []PreviewLink  `json:"links,omitempty"`
	// Entities 与帖子正文使用同一套富文本标注，前端复用 MessageText。
	Entities []PreviewEntity `json:"entities,omitempty"`
	Media    *PreviewMedia   `json:"media,omitempty"`
	// MediaError 是媒体解析失败的原因；评论文字仍然可用。
	MediaError string        `json:"media_error,omitempty"`
	ReplyTo    *CommentReply `json:"reply_to,omitempty"`
}

// CommentsResponse 是 GET /api/v1/links/comments 的响应。
type CommentsResponse struct {
	Link      string `json:"link"`
	MessageID int    `json:"message_id"`
	// ThreadID 是讨论组里对应的线程起始消息 ID；没有评论区时为 0。
	ThreadID int `json:"thread_id,omitempty"`
	// Available 表示帖子确实开启了评论区。
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Chat 是评论区所在的关联讨论组。
	Chat *PreviewChat `json:"chat,omitempty"`
	// PostChat 是原帖所在的频道。
	PostChat *PreviewChat `json:"post_chat,omitempty"`
	Total    int          `json:"total"`
	// Comments 按时间倒序（最新在前）。
	Comments []Comment `json:"comments"`
	// NextOffsetID 传给下一次请求的 offset_id；0 表示没有更多。
	NextOffsetID int `json:"next_offset_id,omitempty"`
}

// handleComments 处理 GET /api/v1/links/comments：
// 读取频道帖子的评论区，只读，不落库。
func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GetComments == nil {
		writeError(w, http.StatusNotImplemented, "not_implemented", "评论区功能未接入")
		return
	}

	link := strings.TrimSpace(r.URL.Query().Get("link"))
	if link == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "link 不能为空")
		return
	}

	limit := defaultCommentsLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit 必须是正整数")
			return
		}
		if value > maxCommentsLimit {
			value = maxCommentsLimit
		}
		limit = value
	}

	offsetID := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset_id")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset_id 必须是非负整数")
			return
		}
		offsetID = value
	}

	resp, err := s.cfg.GetComments(r.Context(), CommentsQuery{
		Link:     link,
		Limit:    limit,
		OffsetID: offsetID,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNoCurrentAccount), errors.Is(err, ErrAccountNoSession):
			writeError(w, http.StatusConflict, "conflict", err.Error())
		case errors.Is(err, ErrCommentsBadRequest):
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "comments_timeout", "读取评论区超时，请稍后重试")
		case errors.Is(err, ErrCommentsFailed):
			writeError(w, http.StatusBadGateway, "comments_failed", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "读取评论区失败: "+err.Error())
		}
		return
	}

	if resp.Comments == nil {
		resp.Comments = []Comment{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// NewCommentsResponse 把 mtproto 的评论结果映射成接口结构。
func NewCommentsResponse(result mtproto.CommentsResult, session mtproto.Session) CommentsResponse {
	resp := CommentsResponse{
		Link:         result.Target.Raw,
		MessageID:    result.PostID,
		ThreadID:     result.ThreadID,
		Available:    result.Available,
		Reason:       result.Reason,
		Total:        result.Total,
		Comments:     make([]Comment, 0, len(result.Comments)),
		NextOffsetID: result.NextOffsetID,
	}
	if result.Chat != nil {
		chat := newPreviewChat(*result.Chat, result.ChatAvatar, session)
		resp.Chat = &chat
	}
	if result.PostChat != nil {
		chat := newPreviewChat(*result.PostChat, result.PostChatAvatar, session)
		resp.PostChat = &chat
	}

	for _, item := range result.Comments {
		comment := Comment{
			ID:         item.ID,
			Date:       item.Date.Unix(),
			Text:       item.Text,
			Links:      newPreviewLinks(item.Links),
			Entities:   newPreviewEntities(item.Entities),
			MediaError: item.MediaError,
		}
		if item.Sender != nil {
			sender := newPreviewSender(item.Sender, session)
			comment.Sender = &sender
		}
		if item.Media != nil {
			comment.Media = newPreviewMedia(item.Media, session)
		}
		if item.ReplyTo != nil {
			comment.ReplyTo = &CommentReply{
				MessageID: item.ReplyTo.MessageID,
				Quote:     item.ReplyTo.Quote,
			}
		}
		resp.Comments = append(resp.Comments, comment)
	}
	return resp
}
