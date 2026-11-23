package database

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestUIDraftSaveGet 覆盖草稿的默认值、覆盖写入与不存在。
func TestUIDraftSaveGet(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.GetUIDraft("downloads"); !errors.Is(err, ErrUIDraftNotFound) {
		t.Fatalf("空库读取草稿应返回 ErrUIDraftNotFound，实际 %v", err)
	}

	saved, err := db.SaveUIDraft("downloads", `{"links":"a"}`)
	if err != nil {
		t.Fatalf("保存草稿失败: %v", err)
	}
	if saved.Key != "downloads" || saved.UpdatedAt.IsZero() {
		t.Fatalf("保存结果异常: %+v", saved)
	}

	got, err := db.GetUIDraft("downloads")
	if err != nil {
		t.Fatalf("读取草稿失败: %v", err)
	}
	if got.Value != `{"links":"a"}` {
		t.Errorf("Value = %q", got.Value)
	}

	if _, err := db.SaveUIDraft("downloads", `{"links":"b"}`); err != nil {
		t.Fatalf("二次保存失败: %v", err)
	}
	got, err = db.GetUIDraft("downloads")
	if err != nil {
		t.Fatalf("二次读取失败: %v", err)
	}
	if got.Value != `{"links":"b"}` {
		t.Errorf("覆盖后 Value = %q", got.Value)
	}
}

// newTestInput 构造一条历史记录。
func newTestInput(hash string) *DownloadInput {
	return &DownloadInput{
		Hash:      hash,
		Links:     "https://t.me/a/1",
		LineCount: 1,
		FirstLine: "https://t.me/a/1",
	}
}

// TestDownloadInputUpsert 确认同一 Hash 只保留一行并累加使用次数。
func TestDownloadInputUpsert(t *testing.T) {
	db := openTestDB(t)

	first := newTestInput("hash-1")
	created, err := db.SaveDownloadInput(first)
	if err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if !created || first.ID == 0 || first.UseCount != 1 {
		t.Fatalf("首次写入结果异常: created=%v %+v", created, first)
	}

	second := newTestInput("hash-1")
	second.Links = "https://t.me/a/1\nhttps://t.me/a/2"
	second.LineCount = 2
	second.CacheJSON = `{"v":1}`
	created, err = db.SaveDownloadInput(second)
	if err != nil {
		t.Fatalf("重复写入失败: %v", err)
	}
	if created {
		t.Fatal("相同 Hash 不应新建记录")
	}
	if second.ID != first.ID {
		t.Errorf("重复写入应回填已有 ID %d，实际 %d", first.ID, second.ID)
	}
	if second.UseCount != 2 || second.LineCount != 2 || second.CacheJSON != `{"v":1}` {
		t.Errorf("重复写入未更新字段: %+v", second)
	}

	list, err := db.ListDownloadInputs(10)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应只有一条记录，实际 %d", len(list))
	}
}

// TestDownloadInputListOrder 确认按最近使用时间倒序。
func TestDownloadInputListOrder(t *testing.T) {
	db := openTestDB(t)

	now := time.Now()
	older := newTestInput("older")
	older.LastUsedAt = now.Add(-2 * time.Hour)
	if _, err := db.SaveDownloadInput(older); err != nil {
		t.Fatal(err)
	}
	newer := newTestInput("newer")
	newer.LastUsedAt = now.Add(-time.Hour)
	if _, err := db.SaveDownloadInput(newer); err != nil {
		t.Fatal(err)
	}
	// 再使用一次旧记录，它应当排到最前。
	again := newTestInput("older")
	again.LastUsedAt = now
	if _, err := db.SaveDownloadInput(again); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListDownloadInputs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != older.ID {
		t.Fatalf("列表顺序异常: %+v", list)
	}
}

// TestDownloadInputDeleteAndPrune 覆盖单条删除、清空与保留最近 N 条。
func TestDownloadInputDeleteAndPrune(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 5; i++ {
		row := newTestInput(fmt.Sprintf("hash-%d", i))
		row.LastUsedAt = time.Now().Add(time.Duration(i) * time.Minute)
		if _, err := db.SaveDownloadInput(row); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.PruneDownloadInputs(3); err != nil {
		t.Fatalf("裁剪失败: %v", err)
	}
	list, err := db.ListDownloadInputs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("裁剪后应剩 3 条，实际 %d: %+v", len(list), list)
	}

	if err := db.DeleteDownloadInput(list[0].ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := db.GetDownloadInput(list[0].ID); !errors.Is(err, ErrDownloadInputNotFound) {
		t.Errorf("删除后应返回 ErrDownloadInputNotFound，实际 %v", err)
	}
	if err := db.DeleteDownloadInput(list[0].ID); !errors.Is(err, ErrDownloadInputNotFound) {
		t.Errorf("重复删除应返回 ErrDownloadInputNotFound，实际 %v", err)
	}

	removed, err := db.DeleteAllDownloadInputs()
	if err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	if removed != 2 {
		t.Errorf("清空条数 = %d，期望 2", removed)
	}
	list, err = db.ListDownloadInputs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("清空后仍有 %d 条", len(list))
	}
}
