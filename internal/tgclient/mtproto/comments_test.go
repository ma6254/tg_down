package mtproto

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestThreadIDFromMessages(t *testing.T) {
	messages := []tg.MessageClass{
		&tg.Message{ID: 10370},
		&tg.MessageService{ID: 10360},
		&tg.Message{ID: 10367},
		&tg.Message{ID: 10369},
	}
	if got := threadIDFromMessages(messages); got != 10367 {
		t.Fatalf("threadID = %d，期望 10367", got)
	}
	if got := threadIDFromMessages(nil); got != 0 {
		t.Fatalf("空列表 threadID = %d，期望 0", got)
	}
}

func TestCommentFromMessage(t *testing.T) {
	reply := &tg.MessageReplyHeader{}
	reply.SetReplyToMsgID(77)
	reply.SetQuoteText("quoted")
	msg := &tg.Message{
		ID:      9,
		Date:    1700000000,
		Message: "hello",
		FromID:  &tg.PeerUser{UserID: 42},
		Media:   nil,
	}
	msg.SetReplyTo(reply)

	users := map[int64]*tg.User{
		42: {ID: 42, FirstName: "Alice", Username: "alice"},
	}
	linked := Peer{Kind: PeerKindChannel, ID: 200, Username: "comments"}
	comment := commentFromMessage(msg, users, nil, linked, 100)

	if comment.ID != 9 || comment.Text != "hello" {
		t.Fatalf("comment = %+v", comment)
	}
	if comment.Date.Unix() != 1700000000 {
		t.Errorf("date = %d", comment.Date.Unix())
	}
	if comment.Sender == nil || comment.Sender.Name != "Alice" || comment.Sender.Username != "alice" {
		t.Errorf("sender = %+v", comment.Sender)
	}
	if comment.ReplyTo == nil || comment.ReplyTo.MessageID != 77 || comment.ReplyTo.Quote != "quoted" {
		t.Errorf("replyTo = %+v", comment.ReplyTo)
	}
	if comment.Media != nil || comment.MediaError != "" {
		t.Errorf("media = %+v, mediaError = %q", comment.Media, comment.MediaError)
	}
}

func TestCommentSenderChannel(t *testing.T) {
	msg := &tg.Message{ID: 10, FromID: &tg.PeerChannel{ChannelID: 200}}
	channels := map[int64]*tg.Channel{
		200: {ID: 200, Title: "评论区", Username: "comments"},
	}
	sender := commentSenderOf(msg, nil, channels)
	if sender == nil {
		t.Fatal("sender 为空")
	}
	if sender.Peer.ID != 200 || sender.Name != "评论区" || sender.Username != "comments" {
		t.Errorf("sender = %+v", sender)
	}
}

func TestCommentFromMessageMediaError(t *testing.T) {
	msg := &tg.Message{
		ID:   11,
		Date: 1700000000,
	}
	msg.SetMedia(&tg.MessageMediaWebPage{})
	comment := commentFromMessage(msg, nil, nil, Peer{}, 100)
	if comment.Media != nil {
		t.Errorf("media = %+v，期望 nil", comment.Media)
	}
	if comment.MediaError == "" {
		t.Error("不支持的媒体应留下 mediaError")
	}
}

func TestCommentCounts(t *testing.T) {
	if got := commentCountOf(&tg.MessagesChannelMessages{Count: 5}); got != 5 {
		t.Errorf("channel messages count = %d", got)
	}
	if got := commentCountOf(&tg.MessagesMessagesSlice{
		Count:    3,
		Messages: []tg.MessageClass{&tg.Message{ID: 1}},
	}); got != 3 {
		t.Errorf("slice count = %d", got)
	}
	if got := commentCountOf(&tg.MessagesMessages{
		Messages: []tg.MessageClass{&tg.Message{ID: 1}},
	}); got != 1 {
		t.Errorf("messages count = %d", got)
	}
}

func TestLastMessageID(t *testing.T) {
	res := &tg.MessagesMessages{
		Messages: []tg.MessageClass{
			&tg.Message{ID: 10},
			&tg.Message{ID: 9},
		},
	}
	if got := lastMessageID(res); got != 9 {
		t.Errorf("lastMessageID = %d，期望 9", got)
	}
	if got := lastMessageID(&tg.MessagesMessages{}); got != 0 {
		t.Errorf("空列表 lastMessageID = %d，期望 0", got)
	}
}

func TestNextCommentsOffset(t *testing.T) {
	tests := []struct {
		name         string
		rawCount     int
		limit        int
		total        int
		commentCount int
		lastID       int
		want         int
	}{
		{"本页没取满", 3, 20, 50, 3, 100, 0},
		{"取满但总数等于评论数", 1, 1, 1, 1, 100, 0},
		{"取满且还有更多", 20, 20, 50, 20, 100, 100},
		{"总数未知时按取满判断", 20, 20, 0, 20, 100, 100},
		{"没有游标", 20, 20, 50, 20, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCommentsOffset(tt.rawCount, tt.limit, tt.total, tt.commentCount, tt.lastID); got != tt.want {
				t.Errorf("nextCommentsOffset = %d，期望 %d", got, tt.want)
			}
		})
	}
}
