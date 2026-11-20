package account

import (
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestAppCredentials 确认 settings 里的 api_id/api_hash 会被账号服务读取。
func TestAppCredentials(t *testing.T) {
	svc := newTestService(t)

	_, custom, err := svc.App()
	if err != nil {
		t.Fatalf("App() 报错: %v", err)
	}
	if custom {
		t.Fatal("未配置时不应返回自定义凭据")
	}

	desktop, err := mtproto.PresetApp(mtproto.AppDesktop)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := svc.AppOrDefault()
	if err != nil {
		t.Fatalf("AppOrDefault() 报错: %v", err)
	}
	if fallback != desktop {
		t.Errorf("未配置时应回退 desktop，实际 %+v", fallback)
	}

	store := config.NewStore(svc.db)
	if err := store.Set(config.KeyAPIID, "123456"); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 32)
	if err := store.Set(config.KeyAPIHash, hash); err != nil {
		t.Fatal(err)
	}

	// 开关未打开时仍然回退 desktop。
	if _, custom, err := svc.App(); err != nil || custom {
		t.Fatalf("api_custom_enabled=false 时不应使用自定义凭据: custom=%v err=%v", custom, err)
	}

	if err := store.Set(config.KeyAPICustomEnabled, "true"); err != nil {
		t.Fatal(err)
	}

	app, custom, err := svc.App()
	if err != nil {
		t.Fatalf("App() 报错: %v", err)
	}
	if !custom || app.ID != 123456 || app.Hash != hash {
		t.Fatalf("自定义凭据不正确: %+v custom=%v", app, custom)
	}

	got, err := svc.AppOrDefault()
	if err != nil {
		t.Fatalf("AppOrDefault() 报错: %v", err)
	}
	if got != app {
		t.Errorf("AppOrDefault() = %+v，期望 %+v", got, app)
	}
}

// TestAppPartialConfig 启用后只配置一项时返回明确错误。
func TestAppPartialConfig(t *testing.T) {
	svc := newTestService(t)

	store := config.NewStore(svc.db)
	if err := store.Set(config.KeyAPICustomEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(config.KeyAPIID, "123456"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.App(); err == nil {
		t.Fatal("只有 api_id 时应当报错")
	}
}

// TestAppEnabledWithoutCredentials 启用开关但没填凭据时返回错误。
func TestAppEnabledWithoutCredentials(t *testing.T) {
	svc := newTestService(t)

	if err := config.NewStore(svc.db).Set(config.KeyAPICustomEnabled, "true"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := svc.App(); err == nil {
		t.Fatal("启用后缺少凭据应当报错")
	}
}
