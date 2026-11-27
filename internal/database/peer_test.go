package database

import (
	"testing"
	"time"
)

// openPeerStore 建一个内存库并返回会话缓存仓库。
func openPeerStore(t *testing.T) *PeerStore {
	t.Helper()
	db, err := Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Install(); err != nil {
		t.Fatalf("建表: %v", err)
	}
	return NewPeerStore(db)
}

// TestPeerStoreSaveUpsert 确认同一 (账号, 键) 只留一行且字段被覆盖。
func TestPeerStoreSaveUpsert(t *testing.T) {
	store := openPeerStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	row := PeerCache{
		AccountID: 1, LookupKey: "u:chan",
		Kind: "channel", PeerID: 42, AccessHash: 7,
		Title: "旧标题", MaxSize: 1024,
		FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := store.SavePeers([]PeerCache{row}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}

	row.Title = "新标题"
	row.MaxSize = 4096
	if err := store.SavePeers([]PeerCache{row}); err != nil {
		t.Fatalf("覆盖写入: %v", err)
	}

	got, err := store.LoadPeers(1, []string{"u:chan"})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("同一主键应只留一行，实际 %d 行", len(got))
	}
	if got["u:chan"].Title != "新标题" || got["u:chan"].MaxSize != 4096 {
		t.Errorf("行 = %+v，期望被覆盖", got["u:chan"])
	}
}

// TestPeerStoreAccountScoped 确认会话缓存按账号隔离。
func TestPeerStoreAccountScoped(t *testing.T) {
	store := openPeerStore(t)
	now := time.Now().UTC()

	if err := store.SavePeers([]PeerCache{{
		AccountID: 1, LookupKey: "u:chan",
		Kind: "channel", PeerID: 42,
		FetchedAt: now, ExpiresAt: now.Add(time.Hour),
	}}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	if got, _ := store.LoadPeers(2, []string{"u:chan"}); len(got) != 0 {
		t.Errorf("另一个账号不应读到，实际 %d 行", len(got))
	}
	if got, _ := store.LoadPeers(1, []string{"u:chan"}); len(got) != 1 {
		t.Errorf("本账号应读到 1 行，实际 %d 行", len(got))
	}
}

// TestPeerStorePruneExpired 确认按过期时间清理，未过期的不受影响。
func TestPeerStorePruneExpired(t *testing.T) {
	store := openPeerStore(t)
	now := time.Now().UTC()

	if err := store.SavePeers([]PeerCache{
		{AccountID: 1, LookupKey: "u:old", Kind: "channel", PeerID: 1,
			FetchedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{AccountID: 1, LookupKey: "u:new", Kind: "channel", PeerID: 2,
			FetchedAt: now, ExpiresAt: now.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	removed, err := store.PruneExpired(now, 10)
	if err != nil {
		t.Fatalf("清理: %v", err)
	}
	if removed != 1 {
		t.Errorf("删除 %d 行，期望 1 行", removed)
	}
	if got, _ := store.LoadPeers(1, []string{"u:old", "u:new"}); len(got) != 1 {
		t.Errorf("剩余 %d 行，期望 1 行", len(got))
	}
}

// TestPeerStoreDelete 确认按账号与键删除。
func TestPeerStoreDelete(t *testing.T) {
	store := openPeerStore(t)
	now := time.Now().UTC()

	if err := store.SavePeers([]PeerCache{
		{AccountID: 1, LookupKey: "u:a", Kind: "channel", PeerID: 1, FetchedAt: now, ExpiresAt: now.Add(time.Hour)},
		{AccountID: 1, LookupKey: "u:b", Kind: "channel", PeerID: 2, FetchedAt: now, ExpiresAt: now.Add(time.Hour)},
	}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	removed, err := store.DeletePeers(1, []string{"u:a"})
	if err != nil {
		t.Fatalf("删除: %v", err)
	}
	if removed != 1 {
		t.Errorf("删除 %d 行，期望 1 行", removed)
	}
	if got, _ := store.LoadPeers(1, []string{"u:a", "u:b"}); len(got) != 1 {
		t.Errorf("剩余 %d 行，期望 1 行", len(got))
	}
}
