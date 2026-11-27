package msgcache

import (
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// newTestCache 建一个内存库上的缓存，时钟固定在 baseTime。
func newTestCache(t *testing.T) (*Cache, func(time.Time)) {
	t.Helper()
	db, err := database.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Install(); err != nil {
		t.Fatalf("建表: %v", err)
	}

	cache := New(db)
	cache.now = func() time.Time { return baseTime }
	return cache, func(at time.Time) { cache.now = func() time.Time { return at } }
}

// sampleResult 构造一条带缩略图与正文的结果，用于验证编解码。
func sampleResult() mtproto.LinkResult {
	return mtproto.LinkResult{
		Target:    link.Target{Raw: "https://t.me/chan/7", Username: "chan", MessageID: 7},
		Chat:      mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 42, Title: "频道"},
		MessageID: 7,
		Text:      "正文",
		Media: &mtproto.Media{
			MessageID: 7,
			Kind:      mtproto.MediaKindPhoto,
			FileName:  "a.jpg",
			Size:      1024,
			Thumb: &mtproto.Thumb{
				Kind:          "photo",
				FileReference: []byte{1, 2, 3},
			},
		},
	}
}

// TestSaveLookupRoundtrip 确认快照写入后能原样读回，未过期时 Fresh 为真。
func TestSaveLookupRoundtrip(t *testing.T) {
	cache, _ := newTestCache(t)
	if err := cache.Save(42, 1, []mtproto.LinkResult{sampleResult()}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	entries, err := cache.Lookup(42, 1, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	entry, ok := entries[7]
	if !ok {
		t.Fatal("未命中缓存")
	}
	if !entry.Fresh {
		t.Error("刚写入的快照应当未过期")
	}
	if entry.Status != database.MessageStatusOK {
		t.Errorf("status = %q，期望 ok", entry.Status)
	}
	if entry.Result.Text != "正文" {
		t.Errorf("text = %q，期望 正文", entry.Result.Text)
	}
	if entry.Result.Media == nil || entry.Result.Media.Thumb == nil {
		t.Fatal("媒体或缩略图丢失")
	}
	if got := entry.Result.Media.Thumb.FileReference; string(got) != "\x01\x02\x03" {
		t.Errorf("file_reference = %v，期望原样保留", got)
	}
	if entry.Result.Chat.Title != "频道" || entry.Result.Chat.ID != 42 {
		t.Errorf("会话信息丢失: %+v", entry.Result.Chat)
	}
	if !entry.FetchedAt.Equal(baseTime) {
		t.Errorf("fetched_at = %v，期望 %v", entry.FetchedAt, baseTime)
	}
}

// TestLookupExpired 确认过期只是让 Fresh 变假，内容仍然可读（用于降级）。
func TestLookupExpired(t *testing.T) {
	cache, setNow := newTestCache(t)
	if err := cache.Save(42, 1, []mtproto.LinkResult{sampleResult()}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	setNow(baseTime.Add(TTL + time.Minute))
	entries, err := cache.Lookup(42, 1, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	entry, ok := entries[7]
	if !ok {
		t.Fatal("过期快照不应被丢弃")
	}
	if entry.Fresh {
		t.Error("超过 TTL 后 Fresh 应为假")
	}
	if entry.Result.Text != "正文" {
		t.Errorf("text = %q，过期快照应保留正文", entry.Result.Text)
	}
}

// TestMarkGoneKeepsSnapshotAndIsAccountScoped 确认标记失效保留正文，
// 且只对做出该判断的账号生效。
func TestMarkGoneKeepsSnapshotAndIsAccountScoped(t *testing.T) {
	cache, _ := newTestCache(t)
	if err := cache.Save(42, 1, []mtproto.LinkResult{sampleResult()}); err != nil {
		t.Fatalf("写入: %v", err)
	}
	if err := cache.MarkGone(42, 1, []int{7}); err != nil {
		t.Fatalf("标记失效: %v", err)
	}

	entries, err := cache.Lookup(42, 1, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	entry, ok := entries[7]
	if !ok {
		t.Fatal("同账号应能看到失效标记")
	}
	if entry.Status != database.MessageStatusGone {
		t.Errorf("status = %q，期望 gone", entry.Status)
	}
	if entry.Result.Text != "正文" {
		t.Errorf("text = %q，标记失效不应清空正文", entry.Result.Text)
	}

	// 另一个账号的「不可见」不能代表当前账号，按未命中处理。
	entries, err = cache.Lookup(42, 2, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	if _, ok := entries[7]; ok {
		t.Error("其它账号的失效标记不应命中")
	}
}

// TestMarkGoneWithoutSnapshot 确认没有快照时也会留下一条负缓存记录。
func TestMarkGoneWithoutSnapshot(t *testing.T) {
	cache, _ := newTestCache(t)
	if err := cache.MarkGone(42, 1, []int{7}); err != nil {
		t.Fatalf("标记失效: %v", err)
	}

	entries, err := cache.Lookup(42, 1, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	entry, ok := entries[7]
	if !ok {
		t.Fatal("负缓存应当命中")
	}
	if entry.Status != database.MessageStatusGone {
		t.Errorf("status = %q，期望 gone", entry.Status)
	}
	if entry.Result.Text != "" {
		t.Errorf("text = %q，没有快照时应为空", entry.Result.Text)
	}
}
