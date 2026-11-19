package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// lastProbeProxy 记录假探测收到的代理地址。
var lastProbeProxy string

// fakeProbeDesktop 用假数据替换真实探测，避免测试连接 Telegram。
func fakeProbeDesktop(t *testing.T) {
	t.Helper()
	orig := probeDesktop
	t.Cleanup(func() { probeDesktop = orig })
	probeDesktop = func(_ context.Context, opts mtproto.DesktopProbeOptions) (*mtproto.DesktopProbeResult, error) {
		lastProbeProxy = opts.Proxy
		return &mtproto.DesktopProbeResult{
			TDataPath: `C:\fake\tdata`,
			Accounts: []mtproto.DesktopAccount{{
				Index:   0,
				UserID:  42,
				Account: &mtproto.Account{ID: 42, FirstName: "Test", LastName: "User", Phone: "+8613800000001"},
				Devices: []mtproto.Device{{Current: true, AppName: "Telegram Desktop", Platform: "Windows"}},
				Session: &session.Data{},
			}},
		}, nil
	}
}

// TestProbeLoginImport 确认默认会写 session 文件、登记数据库并自动选中唯一账号。
func TestProbeLoginImport(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	work := t.TempDir()
	t.Chdir(work)
	fakeProbeDesktop(t)

	resp, err := probeLogin(context.Background(), server.ProbeRequest{})
	if err != nil {
		t.Fatalf("probeLogin: %v", err)
	}
	if resp.Imported != 1 || resp.Skipped != 0 {
		t.Errorf("imported = %d，skipped = %d，期望 1/0", resp.Imported, resp.Skipped)
	}
	if len(resp.Accounts) != 1 || resp.Accounts[0].Name != "Test User" {
		t.Fatalf("accounts = %+v", resp.Accounts)
	}
	if resp.SelectedAccountID == 0 {
		t.Error("唯一账号应被自动选中")
	}

	files, err := filepath.Glob(filepath.Join(work, "sessions", "mtproto-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("sessions 下应有 1 个 session 文件，实际 %v", files)
	}
	if !strings.Contains(files[0], "8613800000001") {
		t.Errorf("session 文件名应包含手机号，实际 %s", files[0])
	}
}

// TestProbeLoginDryRun 确认 import=false 时只探测、不落盘。
func TestProbeLoginDryRun(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	work := t.TempDir()
	t.Chdir(work)
	fakeProbeDesktop(t)

	dryRun := false
	resp, err := probeLogin(context.Background(), server.ProbeRequest{Import: &dryRun})
	if err != nil {
		t.Fatalf("probeLogin: %v", err)
	}
	if resp.Imported != 0 || resp.Skipped != 0 {
		t.Errorf("dry-run 不应导入，实际 imported = %d，skipped = %d", resp.Imported, resp.Skipped)
	}
	if len(resp.Accounts) != 1 {
		t.Fatalf("仍应返回探测结果，accounts = %+v", resp.Accounts)
	}
	files, err := filepath.Glob(filepath.Join(work, "sessions", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("dry-run 不应写 session 文件，实际 %v", files)
	}
}

// TestProbeLoginUsesProxyToggle 确认 HTTP 探测也遵守 http_proxy_enabled。
func TestProbeLoginUsesProxyToggle(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	fakeProbeDesktop(t)
	lastProbeProxy = ""

	seed := func(enabled string) {
		db, err := utils.OpenDatabase()
		if err != nil {
			t.Fatalf("打开数据库失败: %v", err)
		}
		store := config.NewStore(db)
		if err := store.Set(config.KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
			db.Close()
			t.Fatalf("写入代理失败: %v", err)
		}
		if err := store.Set(config.KeyHTTPProxyEnabled, enabled); err != nil {
			db.Close()
			t.Fatalf("写入代理开关失败: %v", err)
		}
		db.Close()
	}

	dryRun := false
	seed("false")
	if _, err := probeLogin(context.Background(), server.ProbeRequest{Import: &dryRun}); err != nil {
		t.Fatalf("probeLogin: %v", err)
	}
	if lastProbeProxy != "" {
		t.Errorf("关闭代理后探测应直连，实际代理 %q", lastProbeProxy)
	}

	seed("true")
	if _, err := probeLogin(context.Background(), server.ProbeRequest{Import: &dryRun}); err != nil {
		t.Fatalf("probeLogin: %v", err)
	}
	if lastProbeProxy != "http://127.0.0.1:7897" {
		t.Errorf("启用代理后应传入代理，实际 %q", lastProbeProxy)
	}
}

// TestProbeLoginMissingTData 确认真实实现遇到不存在的 tdata 时尽早失败，
// 不会去连接 Telegram（测试环境无外网）。
func TestProbeLoginMissingTData(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	missing := filepath.Join(t.TempDir(), "tdata")
	_, err := probeLogin(context.Background(), server.ProbeRequest{TDataPath: missing})
	if err == nil {
		t.Fatal("tdata 不存在时应返回错误")
	}
	if !strings.Contains(err.Error(), "tdata") {
		t.Errorf("错误 = %q，期望提示 tdata 路径", err)
	}
}
