package msgcache

import (
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestPeerKey 确认链接到会话缓存的键：评论链接与帖子链接要分开。
func TestPeerKey(t *testing.T) {
	cases := []struct {
		name   string
		target link.Target
		want   string
		ok     bool
	}{
		{"用户名统一小写", link.Target{Username: "Chan", MessageID: 1}, "u:chan", true},
		{"私有频道 ID", link.Target{ChannelID: 123, MessageID: 1}, "c:123", true},
		{"用户名的评论链接", link.Target{Username: "chan", MessageID: 1, CommentID: 2}, "u:chan#c", true},
		{"频道 ID 的评论链接", link.Target{ChannelID: 123, MessageID: 1, CommentID: 2}, "c:123#c", true},
		{"无法定位", link.Target{MessageID: 1}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PeerKey(tc.target)
			if ok != tc.ok || got != tc.want {
				t.Errorf("PeerKey = (%q, %v)，期望 (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSaveLoadPeers 确认会话定位结果的往返、账号隔离、有效期与单文件上限。
func TestSaveLoadPeers(t *testing.T) {
	cache, setNow := newTestCache(t)
	target := link.Target{Raw: "https://t.me/chan/7", Username: "chan", MessageID: 7}
	peer := mtproto.Peer{
		Kind:       mtproto.PeerKindChannel,
		ID:         42,
		AccessHash: 99,
		Title:      "频道",
		Username:   "chan",
		Broadcast:  true,
	}

	if err := cache.SavePeers(1, mtproto.MaxFileSizePremium,
		[]PeerEntry{{Key: "u:chan", Peer: peer}}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	peers, maxSize, err := cache.LoadPeers(1, []link.Target{target})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	got, ok := peers[0]
	if !ok {
		t.Fatal("未命中会话缓存")
	}
	if got.ID != 42 || got.AccessHash != 99 || got.Title != "频道" || !got.Broadcast {
		t.Errorf("peer = %+v，字段未完整往返", got)
	}
	if maxSize != mtproto.MaxFileSizePremium {
		t.Errorf("max_size = %d，期望 %d", maxSize, mtproto.MaxFileSizePremium)
	}

	// 会话定位带账号：换账号必须重新解析。
	if other, _, _ := cache.LoadPeers(2, []link.Target{target}); len(other) != 0 {
		t.Errorf("其它账号不应命中，实际 %d 条", len(other))
	}

	// 超过有效期后不再命中。
	setNow(baseTime.Add(PeerTTL + time.Hour))
	if stale, _, _ := cache.LoadPeers(1, []link.Target{target}); len(stale) != 0 {
		t.Errorf("过期后仍命中 %d 条", len(stale))
	}
}

// TestForgetPeers 确认可以主动删除失效的会话定位结果。
func TestForgetPeers(t *testing.T) {
	cache, _ := newTestCache(t)
	target := link.Target{Username: "chan", MessageID: 7}
	if err := cache.SavePeers(1, mtproto.MaxFileSize,
		[]PeerEntry{{Key: "u:chan", Peer: mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 42}}}); err != nil {
		t.Fatalf("写入: %v", err)
	}
	if err := cache.ForgetPeers(1, []string{"u:chan"}); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if peers, _, _ := cache.LoadPeers(1, []link.Target{target}); len(peers) != 0 {
		t.Errorf("删除后仍命中 %d 条", len(peers))
	}
}
