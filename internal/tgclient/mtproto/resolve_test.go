package mtproto

import (
	"testing"

	"github.com/ma6254/tg_down/internal/link"
)

// TestMediaLink 确认评论媒体保留原始 ?comment= 链接，普通消息用消息链接。
func TestMediaLink(t *testing.T) {
	peer := Peer{Kind: PeerKindChannel, ID: 3793707790}

	comment := link.Target{
		Raw:       "https://t.me/JPD233/4045?comment=31612",
		Username:  "jpd233",
		MessageID: 4045,
		CommentID: 31612,
	}
	if got := mediaLink(comment, &Media{MessageID: 31612}, peer); got != comment.Raw {
		t.Errorf("评论媒体 = %q，期望保留原始链接 %q", got, comment.Raw)
	}
	// 评论里的相册成员不是评论消息本身，仍用各自的消息链接。
	if got := mediaLink(comment, &Media{MessageID: 31613}, peer); got == comment.Raw {
		t.Errorf("相册成员不应复用评论链接")
	}

	post := link.Target{Raw: "https://t.me/JPD233/4045", Username: "jpd233", MessageID: 4045}
	if got, want := mediaLink(post, &Media{MessageID: 4045}, peer), peer.MessageURL(4045); got != want {
		t.Errorf("普通消息 = %q，期望 %q", got, want)
	}
}
