package config

import (
	"errors"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
)

// applyStore 构造一个空配置的 Store。
func applyStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("建表: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

// TestApplyUnknownKey 确认未知键被拒绝。
func TestApplyUnknownKey(t *testing.T) {
	store := applyStore(t)
	value := "x"
	if _, err := store.Apply(map[string]*string{"nope": &value}); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("错误 = %v，期望 ErrInvalidSetting", err)
	}
}

// TestApplyValidatesBeforeWrite 确认批量更新时一项非法则整体不写。
func TestApplyValidatesBeforeWrite(t *testing.T) {
	store := applyStore(t)
	proxy := "http://127.0.0.1:7897"
	bad := "yes"
	patch := map[string]*string{
		KeyHTTPProxy:        &proxy,
		KeyHTTPProxyEnabled: &bad,
	}
	if _, err := store.Apply(patch); !errors.Is(err, ErrInvalidSetting) {
		t.Fatalf("错误 = %v，期望 ErrInvalidSetting", err)
	}
	if _, ok, err := store.Get(KeyHTTPProxy); err != nil || ok {
		t.Errorf("校验失败时不应写入任何键: ok=%v err=%v", ok, err)
	}
}

// TestApplySetAndUnset 覆盖写入、删除与返回全量列表。
func TestApplySetAndUnset(t *testing.T) {
	store := applyStore(t)
	proxy := "http://127.0.0.1:7897"

	entries, err := store.Apply(map[string]*string{KeyHTTPProxy: &proxy})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Key == KeyHTTPProxy && entry.Value == proxy {
			found = true
		}
	}
	if !found {
		t.Errorf("返回列表缺少写入项: %+v", entries)
	}

	if _, err := store.Apply(map[string]*string{KeyHTTPProxy: nil}); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if _, ok, err := store.Get(KeyHTTPProxy); err != nil || ok {
		t.Errorf("删除后不应存在: ok=%v err=%v", ok, err)
	}
}

// TestApplyMaskedProxy 确认 *** 用现有密码回填，且不改动调用方的 map。
func TestApplyMaskedProxy(t *testing.T) {
	store := applyStore(t)
	raw := "http://user:secret@127.0.0.1:7897"
	if _, err := store.Apply(map[string]*string{KeyHTTPProxy: &raw}); err != nil {
		t.Fatalf("写入代理: %v", err)
	}

	masked := "http://user:***@127.0.0.1:8080"
	patch := map[string]*string{KeyHTTPProxy: &masked}
	if _, err := store.Apply(patch); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, _, err := store.Get(KeyHTTPProxy); err != nil || got != "http://user:secret@127.0.0.1:8080" {
		t.Errorf("库内代理 = %q err=%v", got, err)
	}
	// 调用方传进来的值不该被就地改写。
	if *patch[KeyHTTPProxy] != masked {
		t.Errorf("调用方 patch 被改写为 %q", *patch[KeyHTTPProxy])
	}
}

// TestApplyEmptyPatch 确认空 patch 只返回当前配置。
func TestApplyEmptyPatch(t *testing.T) {
	store := applyStore(t)
	entries, err := store.Apply(nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("空库应返回空列表，实际 %+v", entries)
	}
}
