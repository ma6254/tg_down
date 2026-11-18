package config

import (
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
)

// openStore 创建一个内存数据库与配置存储。
func openStore(t *testing.T) *Store {
	t.Helper()

	db, err := database.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return NewStore(db)
}

// TestValidateProxy 覆盖代理取值校验。
func TestValidateProxy(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:7897",
		"https://proxy.example.com:443",
		"socks5://127.0.0.1:1080",
		"socks5h://user:pass@127.0.0.1:1080",
	}
	for _, raw := range valid {
		if err := ValidateProxy(raw); err != nil {
			t.Errorf("ValidateProxy(%q) = %v，期望通过", raw, err)
		}
	}

	invalid := []struct {
		raw string
	}{
		{""},
		{"   "},
		{"ftp://127.0.0.1:21"},
		{"socks5://"},
		{"://bad"},
	}
	for _, tt := range invalid {
		if err := ValidateProxy(tt.raw); err == nil {
			t.Errorf("ValidateProxy(%q) 应报错", tt.raw)
		}
	}
}

// TestMask 确认代理密码在展示时被隐藏。
func TestMask(t *testing.T) {
	masked := Mask(KeyHTTPProxy, "http://user:pass@127.0.0.1:7897")
	if strings.Contains(masked, "pass") {
		t.Errorf("密码未隐藏: %s", masked)
	}
	plain := "http://127.0.0.1:7897"
	if got := Mask(KeyHTTPProxy, plain); got != plain {
		t.Errorf("无密码时不应改变: %s", got)
	}
	if got := Mask("other", "value"); got != "value" {
		t.Errorf("非代理配置不应处理: %s", got)
	}
}

// TestStoreCRUD 覆盖配置项的读写、列表与删除。
func TestStoreCRUD(t *testing.T) {
	store := openStore(t)

	if _, ok, err := store.Get(KeyHTTPProxy); err != nil || ok {
		t.Fatalf("未配置时应 ok=false，实际 ok=%v err=%v", ok, err)
	}

	if err := store.Set(KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	value, ok, err := store.Get(KeyHTTPProxy)
	if err != nil || !ok {
		t.Fatalf("读取失败: ok=%v err=%v", ok, err)
	}
	if value != "http://127.0.0.1:7897" {
		t.Errorf("value = %q", value)
	}

	// 非法值应被拒绝且不覆盖旧值。
	if err := store.Set(KeyHTTPProxy, "ftp://x"); err == nil {
		t.Error("非法代理应报错")
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(entries) != 1 || !entries[0].Known {
		t.Fatalf("列表结果异常: %+v", entries)
	}

	if err := store.Unset(KeyHTTPProxy); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok, err := store.Get(KeyHTTPProxy); err != nil || ok {
		t.Errorf("删除后应 ok=false，实际 ok=%v err=%v", ok, err)
	}
}

// TestStoreUnknownKey 确认未知配置项统一报错。
func TestStoreUnknownKey(t *testing.T) {
	store := openStore(t)

	if _, _, err := store.Get("theme"); err == nil {
		t.Error("未知键读取应报错")
	}
	if err := store.Set("theme", "dark"); err == nil {
		t.Error("未知键写入应报错")
	}
	if err := store.Unset("theme"); err == nil {
		t.Error("未知键删除应报错")
	}
}

// TestLoad 确认 Load 返回配置快照，缺失项为空。
func TestLoad(t *testing.T) {
	store := openStore(t)

	settings, err := Load(store.db)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if settings.HTTPProxy != "" {
		t.Errorf("HTTPProxy = %q", settings.HTTPProxy)
	}

	if err := store.Set(KeyHTTPProxy, "socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	settings, err = Load(store.db)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if settings.HTTPProxy != "socks5://127.0.0.1:1080" {
		t.Errorf("HTTPProxy = %q", settings.HTTPProxy)
	}
}
