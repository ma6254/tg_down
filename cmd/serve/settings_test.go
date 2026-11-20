package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/server"
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

func findSetting(t *testing.T, settings []server.Setting, key string) server.Setting {
	t.Helper()
	for _, setting := range settings {
		if setting.Key == key {
			return setting
		}
	}
	t.Fatalf("配置项 %s 不存在: %+v", key, settings)
	return server.Setting{}
}

// TestSettingsProxyMaskedAndMerged 覆盖 API 脱敏与 *** 回填。
func TestSettingsProxyMaskedAndMerged(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()
	raw := "http://user:secret@127.0.0.1:7897"
	seedProxy(t, raw)

	settings, err := getSettings(ctx)
	if err != nil {
		t.Fatalf("getSettings: %v", err)
	}
	proxy := findSetting(t, settings, config.KeyHTTPProxy)
	if want := "http://user:***@127.0.0.1:7897"; proxy.Value != want {
		t.Errorf("API 代理值 = %q，期望 %q", proxy.Value, want)
	}
	if proxy.Type != config.TypeString {
		t.Errorf("代理 type = %q，期望 %q", proxy.Type, config.TypeString)
	}
	if proxy.Default != "" {
		t.Errorf("代理 default = %q，期望空", proxy.Default)
	}
	if strings.Contains(proxy.Value, "secret") {
		t.Error("API 不应返回代理密码明文")
	}

	// 只改端口、密码保持 ***：服务端应回填真实密码。
	masked := "http://user:***@127.0.0.1:8080"
	settings, err = updateSettings(ctx, server.SettingsPatch{
		config.KeyHTTPProxy: &masked,
	})
	if err != nil {
		t.Fatalf("updateSettings: %v", err)
	}
	proxy = findSetting(t, settings, config.KeyHTTPProxy)
	if want := "http://user:***@127.0.0.1:8080"; proxy.Value != want {
		t.Errorf("更新后 API 值 = %q，期望 %q", proxy.Value, want)
	}
	if got, want := readProxy(t), "http://user:secret@127.0.0.1:8080"; got != want {
		t.Errorf("库内代理 = %q，期望 %q", got, want)
	}

	// 清除后配置数组为空。
	settings, err = updateSettings(ctx, server.SettingsPatch{
		config.KeyHTTPProxy: nil,
	})
	if err != nil {
		t.Fatalf("清除代理失败: %v", err)
	}
	if len(settings) != 0 {
		t.Errorf("清除后仍有配置: %+v", settings)
	}
}

// TestSettingsExposeTypes 确认 API 返回配置项的逻辑类型。
func TestSettingsExposeTypes(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	seedProxy(t, "http://127.0.0.1:7897")

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	store := config.NewStore(db)
	if err := store.SetCurrentAccountID(7); err != nil {
		db.Close()
		t.Fatalf("写入当前账号失败: %v", err)
	}
	if err := store.Set(config.KeyHTTPProxyEnabled, "false"); err != nil {
		db.Close()
		t.Fatalf("写入代理开关失败: %v", err)
	}
	db.Close()

	settings, err := getSettings(context.Background())
	if err != nil {
		t.Fatalf("getSettings: %v", err)
	}
	if got := findSetting(t, settings, config.KeyHTTPProxy).Type; got != config.TypeString {
		t.Errorf("http_proxy type = %q", got)
	}
	if got := findSetting(t, settings, config.KeyCurrentAccountID).Type; got != config.TypeInteger {
		t.Errorf("current_account_id type = %q", got)
	}
	toggle := findSetting(t, settings, config.KeyHTTPProxyEnabled)
	if toggle.Type != config.TypeBoolean {
		t.Errorf("http_proxy_enabled type = %q", toggle.Type)
	}
	if toggle.Default != "true" {
		t.Errorf("http_proxy_enabled default = %q，期望 true", toggle.Default)
	}
	if toggle.Value != "false" {
		t.Errorf("http_proxy_enabled value = %q", toggle.Value)
	}
}

// TestSettingsMaskedProxyRequiresPassword 确认没有旧密码时拒绝 ***。
func TestSettingsMaskedProxyRequiresPassword(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	masked := "http://user:***@127.0.0.1:7897"

	_, err := updateSettings(context.Background(), server.SettingsPatch{
		config.KeyHTTPProxy: &masked,
	})
	if !errors.Is(err, server.ErrSettingInvalid) {
		t.Fatalf("期望 ErrSettingInvalid，实际 %v", err)
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if _, ok, err := config.NewStore(db).Get(config.KeyHTTPProxy); err != nil || ok {
		t.Errorf("失败时不应写入代理: ok=%v err=%v", ok, err)
	}
}

// TestSettingsRejectsReadOnly 确认 current_account_id 不能通过配置接口写。
func TestSettingsRejectsReadOnly(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	value := "1"

	_, err := updateSettings(context.Background(), server.SettingsPatch{
		config.KeyCurrentAccountID: &value,
	})
	if !errors.Is(err, server.ErrSettingInvalid) {
		t.Fatalf("期望 ErrSettingInvalid，实际 %v", err)
	}
}
