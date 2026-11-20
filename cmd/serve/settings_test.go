package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/utils"
)

// seedProxy 写入一个带密码的代理配置。
func seedProxy(t *testing.T, value string) {
	t.Helper()
	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if err := config.NewStore(db).Set(config.KeyHTTPProxy, value); err != nil {
		t.Fatalf("写入代理失败: %v", err)
	}
}

// readProxy 直接读取库里的原始代理值。
func readProxy(t *testing.T) string {
	t.Helper()
	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	value, ok, err := config.NewStore(db).Get(config.KeyHTTPProxy)
	if err != nil {
		t.Fatalf("读取代理失败: %v", err)
	}
	if !ok {
		t.Fatal("代理配置不存在")
	}
	return value
}

// findEntry 在配置列表里按 key 查找。
func findEntry(t *testing.T, entries []config.Entry, key string) config.Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("配置项 %s 不存在: %+v", key, entries)
	return config.Entry{}
}

// TestGetSettings 确认读出的配置带类型与说明，值来自数据库。
func TestGetSettings(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	seedProxy(t, "http://127.0.0.1:7897")

	entries, err := getSettings(context.Background())
	if err != nil {
		t.Fatalf("getSettings: %v", err)
	}
	entry := findEntry(t, entries, config.KeyHTTPProxy)
	if entry.Type != config.TypeString {
		t.Errorf("类型 = %q，期望 string", entry.Type)
	}
	if entry.Value != "http://127.0.0.1:7897" {
		t.Errorf("值 = %q（领域层应返回明文）", entry.Value)
	}
	if entry.Description == "" {
		t.Error("已知配置项应带说明")
	}
}

// TestUpdateSettings 覆盖写入、删除与校验失败。
func TestUpdateSettings(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()

	proxy := "http://127.0.0.1:7897"
	entries, err := updateSettings(ctx, map[string]*string{config.KeyHTTPProxy: &proxy})
	if err != nil {
		t.Fatalf("updateSettings: %v", err)
	}
	if got := findEntry(t, entries, config.KeyHTTPProxy).Value; got != proxy {
		t.Errorf("写入后 = %q，期望 %q", got, proxy)
	}

	// 删除：值为 nil。
	entries, err = updateSettings(ctx, map[string]*string{config.KeyHTTPProxy: nil})
	if err != nil {
		t.Fatalf("删除配置: %v", err)
	}
	for _, entry := range entries {
		if entry.Key == config.KeyHTTPProxy {
			t.Errorf("删除后仍存在: %+v", entry)
		}
	}
}

// TestUpdateSettingsRejectsInvalid 确认非法值不会写进库。
func TestUpdateSettingsRejectsInvalid(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	bad := "yes"
	_, err := updateSettings(context.Background(), map[string]*string{config.KeyHTTPProxyEnabled: &bad})
	if !errors.Is(err, config.ErrInvalidSetting) {
		t.Fatalf("错误 = %v，期望 ErrInvalidSetting", err)
	}
}

// TestUpdateSettingsMaskedProxy 确认真实密码能正确回填。
func TestUpdateSettingsMaskedProxy(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()

	seedProxy(t, "http://user:secret@127.0.0.1:7897")

	masked := "http://user:***@127.0.0.1:8080"
	entries, err := updateSettings(ctx, map[string]*string{config.KeyHTTPProxy: &masked})
	if err != nil {
		t.Fatalf("updateSettings: %v", err)
	}
	// 领域层返回的是真实值，脱敏在 internal/server 做。
	if got := findEntry(t, entries, config.KeyHTTPProxy).Value; got != "http://user:secret@127.0.0.1:8080" {
		t.Errorf("回填后 = %q", got)
	}
	if got := readProxy(t); got != "http://user:secret@127.0.0.1:8080" {
		t.Errorf("库内代理 = %q", got)
	}
}

// TestUpdateSettingsMaskedProxyRequiresPassword 确认没有旧密码时拒绝 ***。
func TestUpdateSettingsMaskedProxyRequiresPassword(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	masked := "http://user:***@127.0.0.1:7897"
	_, err := updateSettings(context.Background(), map[string]*string{config.KeyHTTPProxy: &masked})
	if !errors.Is(err, config.ErrInvalidSetting) {
		t.Fatalf("期望 ErrInvalidSetting，实际 %v", err)
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	defer db.Close()
	if _, ok, err := config.NewStore(db).Get(config.KeyHTTPProxy); err != nil || ok {
		t.Errorf("失败时不应写入代理: ok=%v err=%v", ok, err)
	}
}
