package database

import (
	"testing"
	"time"
)

// openMessageStore 建一个内存库并返回消息缓存仓库。
func openMessageStore(t *testing.T) *MessageStore {
	t.Helper()
	db, err := Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Install(); err != nil {
		t.Fatalf("建表: %v", err)
	}
	return NewMessageStore(db)
}

// TestMessageStoreSaveUpsert 确认同一 (chat_id, message_id) 只留一行，
// 可刷新字段被覆盖，last_used_at 不被 Save 改动（它由 Touch 维护）。
func TestMessageStoreSaveUpsert(t *testing.T) {
	store := openMessageStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	row := MessageCache{
		ChatID:        100,
		MessageID:     7,
		Status:        MessageStatusOK,
		StatusAccount: 1,
		Payload:       `{"v":1}`,
		SchemaVer:     1,
		FetchedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
		LastUsedAt:    now,
	}
	if err := store.Save([]MessageCache{row}); err != nil {
		t.Fatalf("首次写入: %v", err)
	}

	row.Payload = `{"v":2}`
	row.LastUsedAt = now.Add(time.Minute)
	if err := store.Save([]MessageCache{row}); err != nil {
		t.Fatalf("覆盖写入: %v", err)
	}

	got, err := store.Lookup(100, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("同一主键应只留一行，实际 %d 行", len(got))
	}
	if got[7].Payload != `{"v":2}` {
		t.Errorf("payload = %q，期望被覆盖", got[7].Payload)
	}
	if !got[7].LastUsedAt.Equal(now) {
		t.Errorf("last_used_at = %v，Save 不应覆盖它", got[7].LastUsedAt)
	}
}

// TestMessageStoreMarkGoneKeepsPayload 确认标记失效不会清掉既有快照，
// 也不会把 fetched_at 改成「确认失效」的时间。
func TestMessageStoreMarkGoneKeepsPayload(t *testing.T) {
	store := openMessageStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	fetched := now.Add(-2 * time.Hour)

	if err := store.Save([]MessageCache{{
		ChatID: 100, MessageID: 7,
		Status: MessageStatusOK, StatusAccount: 1,
		Payload:   `{"text":"正文"}`,
		SchemaVer: 1,
		FetchedAt: fetched, ExpiresAt: now.Add(time.Hour), LastUsedAt: now,
	}}); err != nil {
		t.Fatalf("写入快照: %v", err)
	}

	if err := store.MarkGone([]MessageCache{{
		ChatID: 100, MessageID: 7,
		Status: MessageStatusGone, StatusAccount: 2,
		SchemaVer: 1,
		FetchedAt: now, ExpiresAt: now.Add(time.Minute), LastUsedAt: now,
	}}); err != nil {
		t.Fatalf("标记失效: %v", err)
	}

	got, err := store.Lookup(100, []int{7})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	row := got[7]
	if row.Payload != `{"text":"正文"}` {
		t.Errorf("payload = %q，标记失效不应清空快照", row.Payload)
	}
	if row.Status != MessageStatusGone {
		t.Errorf("status = %q，期望 gone", row.Status)
	}
	if row.StatusAccount != 2 {
		t.Errorf("status_account = %d，期望 2", row.StatusAccount)
	}
	if !row.FetchedAt.Equal(fetched) {
		t.Errorf("fetched_at = %v，应保持快照取得时间 %v", row.FetchedAt, fetched)
	}
}

// TestMessageStorePrune 确认按最近使用时间删除，且只删过期的那部分。
func TestMessageStorePrune(t *testing.T) {
	store := openMessageStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	rows := []MessageCache{
		{ChatID: 100, MessageID: 1, Status: MessageStatusOK, Payload: `{"v":1}`, SchemaVer: 1,
			FetchedAt: now.AddDate(0, 0, -100), ExpiresAt: now, LastUsedAt: now.AddDate(0, 0, -100)},
		{ChatID: 100, MessageID: 2, Status: MessageStatusOK, Payload: `{"v":1}`, SchemaVer: 1,
			FetchedAt: now, ExpiresAt: now.Add(time.Hour), LastUsedAt: now},
	}
	if err := store.Save(rows); err != nil {
		t.Fatalf("写入: %v", err)
	}

	removed, err := store.Prune(now.AddDate(0, 0, -90), 10)
	if err != nil {
		t.Fatalf("清理: %v", err)
	}
	if removed != 1 {
		t.Fatalf("删除 %d 行，期望 1 行", removed)
	}

	got, err := store.Lookup(100, []int{1, 2})
	if err != nil {
		t.Fatalf("读取: %v", err)
	}
	if _, ok := got[1]; ok {
		t.Error("过期行应被删除")
	}
	if _, ok := got[2]; !ok {
		t.Error("未过期行不应被删除")
	}
}
