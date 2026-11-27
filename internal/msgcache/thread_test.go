package msgcache

import (
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestThreadKey 确认评论线程缓存的键覆盖用户名与私有频道两种链接。
func TestThreadKey(t *testing.T) {
	cases := []struct {
		name   string
		target link.Target
		want   string
		ok     bool
	}{
		{"用户名", link.Target{Username: "Chan", MessageID: 7}, "t:u:chan#7", true},
		{"频道 ID", link.Target{ChannelID: 42, MessageID: 7}, "t:c:42#7", true},
		{"没有消息 ID", link.Target{Username: "chan"}, "", false},
		{"无法定位", link.Target{MessageID: 7}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ThreadKey(tc.target)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ThreadKey = (%q, %v)，期望 (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSaveLoadThread 确认线程定位结果往返、账号隔离与有效期。
func TestSaveLoadThread(t *testing.T) {
	cache, setNow := newTestCache(t)
	target := link.Target{Raw: "https://t.me/chan/7", Username: "chan", MessageID: 7}
	thread := mtproto.CommentThread{
		Post:          mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 42, Title: "频道"},
		Chat:          mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 43, Title: "讨论组"},
		ThreadID:      900,
		PostPhotoID:   11,
		LinkedPhotoID: 22,
	}
	if err := cache.SaveThread(1, target, thread); err != nil {
		t.Fatalf("写入: %v", err)
	}

	got := cache.LoadThread(1, target)
	if got == nil {
		t.Fatal("未命中线程缓存")
	}
	if got.ThreadID != 900 || got.Chat.ID != 43 || got.Post.ID != 42 {
		t.Errorf("thread = %+v，字段未完整往返", got)
	}
	if got.PostPhotoID != 11 || got.LinkedPhotoID != 22 {
		t.Errorf("头像 ID 丢失: %+v", got)
	}

	// 按账号隔离。
	if other := cache.LoadThread(2, target); other != nil {
		t.Errorf("其它账号不应命中: %+v", other)
	}

	// 超过有效期后不再命中。
	setNow(baseTime.Add(ThreadTTL + time.Hour))
	if stale := cache.LoadThread(1, target); stale != nil {
		t.Errorf("过期后仍命中: %+v", stale)
	}
}

// TestClearStats 确认规模统计与清空。
func TestClearStats(t *testing.T) {
	cache, _ := newTestCache(t)
	target := link.Target{Username: "chan", MessageID: 7}

	if err := cache.Save(42, 1, []mtproto.LinkResult{sampleResult()}); err != nil {
		t.Fatalf("写入消息: %v", err)
	}
	if err := cache.SavePeers(1, mtproto.MaxFileSize,
		[]PeerEntry{{Key: "u:chan", Peer: mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 42}}}); err != nil {
		t.Fatalf("写入会话: %v", err)
	}
	if err := cache.SaveThread(1, target, mtproto.CommentThread{ThreadID: 900}); err != nil {
		t.Fatalf("写入线程: %v", err)
	}

	stats, err := cache.Stats()
	if err != nil {
		t.Fatalf("统计: %v", err)
	}
	if stats.Messages != 1 || stats.Peers != 1 || stats.Threads != 1 {
		t.Fatalf("统计 = %+v，期望 1/1/1", stats)
	}

	removed, err := cache.Clear()
	if err != nil {
		t.Fatalf("清空: %v", err)
	}
	if removed.Messages != 1 || removed.Peers != 1 || removed.Threads != 1 {
		t.Errorf("删除 = %+v，期望 1/1/1", removed)
	}
	if after, _ := cache.Stats(); after != (Stats{}) {
		t.Errorf("清空后统计 = %+v，期望全 0", after)
	}
}
