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

// TestItemTypes 确认每个登记配置项都声明了有效的逻辑类型与默认值。
func TestItemTypes(t *testing.T) {
	for _, item := range Items() {
		switch item.Type {
		case TypeString, TypeInteger, TypeBoolean:
		default:
			t.Errorf("配置项 %s 的 type=%q 不合法", item.Key, item.Type)
		}
		if item.Type == TypeBoolean {
			if err := ValidateBool(item.Default); err != nil {
				t.Errorf("配置项 %s 的默认值 %q 不合法: %v", item.Key, item.Default, err)
			}
		}
	}

	item, ok := Lookup(KeyHTTPProxyEnabled)
	if !ok || item.Default != "true" {
		t.Errorf("http_proxy_enabled 默认值 = %q, ok=%v，期望 true", item.Default, ok)
	}
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
	if want := "http://user:***@127.0.0.1:7897"; masked != want {
		t.Errorf("Mask = %q，期望 %q", masked, want)
	}
	plain := "http://127.0.0.1:7897"
	if got := Mask(KeyHTTPProxy, plain); got != plain {
		t.Errorf("无密码时不应改变: %s", got)
	}
	if got := Mask("other", "value"); got != "value" {
		t.Errorf("非代理配置不应处理: %s", got)
	}
}

// TestMergeMaskedProxy 确认编辑时代理密码的 *** 会回填为真实密码。
func TestMergeMaskedProxy(t *testing.T) {
	raw := "http://user:secret@127.0.0.1:7897"

	got, err := MergeMaskedProxy(raw, "http://user:***@127.0.0.1:8080")
	if err != nil {
		t.Fatalf("合并失败: %v", err)
	}
	if want := "http://user:secret@127.0.0.1:8080"; got != want {
		t.Errorf("合并结果 = %q，期望 %q", got, want)
	}

	// 没有 *** 时原样返回，不关心 current。
	plain := "socks5://127.0.0.1:1080"
	if got, err := MergeMaskedProxy(raw, plain); err != nil || got != plain {
		t.Errorf("无密码输入 = %q, err=%v", got, err)
	}

	// 没有可回填的旧密码时必须报错，不能把 *** 存成密码。
	if _, err := MergeMaskedProxy("", "http://user:***@127.0.0.1:1080"); err == nil {
		t.Error("缺少旧密码时应报错")
	}
	if _, err := MergeMaskedProxy("http://127.0.0.1:7897", "http://user:***@127.0.0.1:1080"); err == nil {
		t.Error("旧代理无密码时应报错")
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
	if entries[0].Type != TypeString {
		t.Errorf("http_proxy type = %q，期望 %q", entries[0].Type, TypeString)
	}
	if entries[0].Default != "" {
		t.Errorf("http_proxy default = %q，期望空", entries[0].Default)
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

// TestSyncTypesBackfills 确认旧库的类型列会被按 config.items 修正。
func TestSyncTypesBackfills(t *testing.T) {
	store := openStore(t)

	// 模拟旧库：SetSetting 默认写 string，而 current_account_id 应为 integer。
	if err := store.db.SetSetting(KeyCurrentAccountID, "7"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := SyncTypes(store.db); err != nil {
		t.Fatalf("同步类型失败: %v", err)
	}

	rows, err := store.db.ListSettings()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(rows) != 1 || rows[0].Type != TypeInteger {
		t.Errorf("数据库中的类型 = %+v，期望 integer", rows)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != TypeInteger {
		t.Errorf("Entry 类型 = %+v，期望 integer", entries)
	}
}

// TestValidateBool 覆盖布尔配置项校验。
func TestValidateBool(t *testing.T) {
	for _, raw := range []string{"true", "false", "TRUE", " False "} {
		if err := ValidateBool(raw); err != nil {
			t.Errorf("ValidateBool(%q) = %v，期望通过", raw, err)
		}
	}
	for _, raw := range []string{"", "1", "0", "yes", "on"} {
		if err := ValidateBool(raw); err == nil {
			t.Errorf("ValidateBool(%q) 应报错", raw)
		}
	}
}

// TestProxyToggle 覆盖 http_proxy_enabled 对实际代理的影响。
func TestProxyToggle(t *testing.T) {
	store := openStore(t)

	if got, err := store.Proxy(); err != nil || got != "" {
		t.Fatalf("未配置代理时 = %q, err=%v", got, err)
	}

	const proxy = "http://127.0.0.1:7897"
	if err := store.Set(KeyHTTPProxy, proxy); err != nil {
		t.Fatalf("写入代理失败: %v", err)
	}
	if got, err := store.Proxy(); err != nil || got != proxy {
		t.Fatalf("默认应启用代理，实际 %q, err=%v", got, err)
	}

	if err := store.Set(KeyHTTPProxyEnabled, "FALSE"); err != nil {
		t.Fatalf("关闭代理失败: %v", err)
	}
	if value, _, err := store.Get(KeyHTTPProxyEnabled); err != nil || value != "false" {
		t.Errorf("布尔值应归一为 false，实际 %q err=%v", value, err)
	}
	if got, err := store.Proxy(); err != nil || got != "" {
		t.Fatalf("关闭代理后应直连，实际 %q, err=%v", got, err)
	}

	if err := store.Set(KeyHTTPProxyEnabled, "true"); err != nil {
		t.Fatalf("启用代理失败: %v", err)
	}
	if got, err := store.Proxy(); err != nil || got != proxy {
		t.Fatalf("重新启用后 = %q, err=%v", got, err)
	}

	// 清除开关后回到默认启用。
	if err := store.Unset(KeyHTTPProxyEnabled); err != nil {
		t.Fatalf("清除开关失败: %v", err)
	}
	if got, err := store.Proxy(); err != nil || got != proxy {
		t.Fatalf("清除开关后应默认启用，实际 %q, err=%v", got, err)
	}

	if err := store.Set(KeyHTTPProxyEnabled, "yes"); err == nil {
		t.Error("非法布尔值应被拒绝")
	}
}

// TestSeedDefaults 确认空 settings 表会写入非空默认值，已有配置时不补写。
func TestSeedDefaults(t *testing.T) {
	store := openStore(t)

	if err := SeedDefaults(store.db); err != nil {
		t.Fatalf("SeedDefaults 失败: %v", err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	// 目前默认值非空的项：api_custom_enabled（false）、http_proxy_enabled（true）。
	if len(entries) != 2 {
		t.Fatalf("默认配置应有 2 项，实际 %+v", entries)
	}
	byKey := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byKey[entry.Key] = entry
	}
	if got := byKey[KeyHTTPProxyEnabled]; got.Value != "true" || got.Type != TypeBoolean {
		t.Errorf("http_proxy_enabled = %+v", got)
	}
	if got := byKey[KeyAPICustomEnabled]; got.Value != "false" || got.Type != TypeBoolean {
		t.Errorf("api_custom_enabled = %+v", got)
	}

	// 幂等：再次调用不会重复插入。
	if err := SeedDefaults(store.db); err != nil {
		t.Fatalf("再次 SeedDefaults 失败: %v", err)
	}
	if entries, _ := store.List(); len(entries) != 2 {
		t.Errorf("重复调用后应有 2 项，实际 %+v", entries)
	}

	// 已有任意配置时不补默认值。
	other := openStore(t)
	if err := other.Set(KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := SeedDefaults(other.db); err != nil {
		t.Fatalf("SeedDefaults 失败: %v", err)
	}
	entries, _ = other.List()
	if len(entries) != 1 || entries[0].Key != KeyHTTPProxy {
		t.Errorf("已有配置时不应补写，实际 %+v", entries)
	}
}

// TestLoad 确认 Load 返回配置快照，缺失项为空/取默认值。
func TestLoad(t *testing.T) {
	store := openStore(t)

	settings, err := Load(store.db)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if settings.HTTPProxy != "" {
		t.Errorf("HTTPProxy = %q", settings.HTTPProxy)
	}
	if !settings.HTTPProxyEnabled {
		t.Error("http_proxy_enabled 未配置时应默认 true")
	}

	if err := store.Set(KeyHTTPProxy, "socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := store.Set(KeyHTTPProxyEnabled, "false"); err != nil {
		t.Fatalf("关闭代理失败: %v", err)
	}
	settings, err = Load(store.db)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if settings.HTTPProxy != "socks5://127.0.0.1:1080" {
		t.Errorf("HTTPProxy = %q", settings.HTTPProxy)
	}
	if settings.HTTPProxyEnabled {
		t.Error("HTTPProxyEnabled 应为 false")
	}
}
