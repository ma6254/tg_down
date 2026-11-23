package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

func TestNewCommentsResponse(t *testing.T) {
	postChat := mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 100, Title: "频道", Username: "chan", Broadcast: true}
	commentChat := mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 200, Title: "评论区", Username: "comments"}
	senderPeer := mtproto.Peer{Kind: mtproto.PeerKindUser, ID: 300, Title: "Alice", Username: "alice"}
	date := time.Unix(1700000000, 0)

	result := mtproto.CommentsResult{
		Target:       link.Target{Raw: "https://t.me/chan/1", Username: "chan", MessageID: 1},
		Available:    true,
		Chat:         &commentChat,
		PostChat:     &postChat,
		PostID:       1,
		ThreadID:     50,
		Total:        1,
		NextOffsetID: 9,
		Comments: []mtproto.Comment{{
			ID:   9,
			Date: date,
			Sender: &mtproto.Sender{
				Peer:     senderPeer,
				Name:     "Alice",
				Username: "alice",
			},
			Text:     "hello",
			Links:    []mtproto.MessageLink{{Text: "t.me", URL: "https://t.me"}},
			Entities: []mtproto.MessageEntity{{Type: "bold", Offset: 0, Length: 5}},
			ReplyTo:  &mtproto.CommentReply{MessageID: 50, Quote: "quoted"},
		}},
	}

	resp := NewCommentsResponse(result, mtproto.Session{})
	if resp.Link != "https://t.me/chan/1" || resp.MessageID != 1 || !resp.Available {
		t.Fatalf("基础字段 = %+v", resp)
	}
	if resp.Chat == nil || resp.Chat.Title != "评论区" {
		t.Errorf("chat = %+v", resp.Chat)
	}
	if resp.PostChat == nil || resp.PostChat.Broadcast != true {
		t.Errorf("post_chat = %+v", resp.PostChat)
	}
	if len(resp.Comments) != 1 {
		t.Fatalf("comments = %+v", resp.Comments)
	}
	comment := resp.Comments[0]
	if comment.ID != 9 || comment.Date != date.Unix() || comment.Text != "hello" {
		t.Errorf("comment = %+v", comment)
	}
	if comment.Sender == nil || comment.Sender.Name != "Alice" {
		t.Errorf("sender = %+v", comment.Sender)
	}
	if comment.ReplyTo == nil || comment.ReplyTo.MessageID != 50 || comment.ReplyTo.Quote != "quoted" {
		t.Errorf("reply_to = %+v", comment.ReplyTo)
	}
	if len(comment.Links) != 1 || len(comment.Entities) != 1 {
		t.Errorf("links/entities = %+v/%+v", comment.Links, comment.Entities)
	}
}

func TestNewCommentsResponseUnavailable(t *testing.T) {
	result := mtproto.CommentsResult{
		Target:    link.Target{Raw: "https://t.me/chan/1", Username: "chan", MessageID: 1},
		Available: false,
		Reason:    "该帖子没有开启评论",
		PostID:    1,
	}
	resp := NewCommentsResponse(result, mtproto.Session{})
	if resp.Available {
		t.Error("available 应为 false")
	}
	if resp.Reason != "该帖子没有开启评论" {
		t.Errorf("reason = %q", resp.Reason)
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Comments json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Comments) != "[]" {
		t.Errorf("comments = %s，期望 []", raw.Comments)
	}
}
