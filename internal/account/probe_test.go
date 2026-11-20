package account

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/session"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
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

// probeService 构造一个使用临时数据目录的服务，并返回该数据目录。
func probeService(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("TG_DOWN_HOME", home)
	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db), home
}

// TestProbeImport 确认默认会写 session 文件、登记数据库并自动选中唯一账号。
func TestProbeImport(t *testing.T) {
	t.Chdir(t.TempDir())
	fakeProbeDesktop(t)

	svc, home := probeService(t)
	result, err := svc.Probe(context.Background(), ProbeOptions{Import: true})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Imported != 1 || result.Skipped != 0 {
		t.Errorf("imported = %d，skipped = %d，期望 1/0", result.Imported, result.Skipped)
	}
	if len(result.Accounts) != 1 || result.Accounts[0].Name != "Test User" {
		t.Fatalf("accounts = %+v", result.Accounts)
	}
	if result.SelectedAccountID == 0 {
		t.Error("唯一账号应被自动选中")
	}

	// 默认落在 <数据目录>/sessions，而不是当前工作目录。
	files, err := filepath.Glob(filepath.Join(home, "sessions", "mtproto-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("数据目录 sessions 下应有 1 个 session 文件，实际 %v", files)
	}
	if !strings.Contains(files[0], "8613800000001") {
		t.Errorf("session 文件名应包含手机号，实际 %s", files[0])
	}
}

// TestProbeDryRun 确认 Import=false 时只探测、不落盘。
func TestProbeDryRun(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	fakeProbeDesktop(t)

	svc, _ := probeService(t)
	result, err := svc.Probe(context.Background(), ProbeOptions{Import: false})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 {
		t.Errorf("dry-run 不应导入，实际 imported = %d，skipped = %d", result.Imported, result.Skipped)
	}
	if len(result.Accounts) != 1 {
		t.Fatalf("仍应返回探测结果，accounts = %+v", result.Accounts)
	}
	files, err := filepath.Glob(filepath.Join(work, "sessions", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("dry-run 不应写 session 文件，实际 %v", files)
	}
}

// TestProbeUsesProxyToggle 确认探测遵守 http_proxy_enabled。
func TestProbeUsesProxyToggle(t *testing.T) {
	t.Chdir(t.TempDir())
	svc, _ := probeService(t)
	fakeProbeDesktop(t)
	lastProbeProxy = ""

	seed := func(enabled string) {
		store := config.NewStore(svc.db)
		if err := store.Set(config.KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
			t.Fatalf("写入代理失败: %v", err)
		}
		if err := store.Set(config.KeyHTTPProxyEnabled, enabled); err != nil {
			t.Fatalf("写入代理开关失败: %v", err)
		}
	}

	seed("false")
	if _, err := svc.Probe(context.Background(), ProbeOptions{}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if lastProbeProxy != "" {
		t.Errorf("关闭代理后探测应直连，实际代理 %q", lastProbeProxy)
	}

	seed("true")
	if _, err := svc.Probe(context.Background(), ProbeOptions{}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if lastProbeProxy != "http://127.0.0.1:7897" {
		t.Errorf("启用代理后应传入代理，实际 %q", lastProbeProxy)
	}
}

// TestProbeMissingTData 确认真实实现遇到不存在的 tdata 时尽早失败，
// 不会去连接 Telegram（测试环境无外网）。
func TestProbeMissingTData(t *testing.T) {
	svc, _ := probeService(t)

	missing := filepath.Join(t.TempDir(), "tdata")
	_, err := svc.Probe(context.Background(), ProbeOptions{TDataPath: missing, Import: true})
	if err == nil {
		t.Fatal("tdata 不存在时应返回错误")
	}
	if !strings.Contains(err.Error(), "tdata") {
		t.Errorf("错误 = %q，期望提示 tdata 路径", err)
	}
}

// TestLogoutDesktopRejected 确认桌面导入的账号不允许注销。
func TestLogoutDesktopRejected(t *testing.T) {
	svc, _ := probeService(t)
	row, err := svc.Record(RecordInput{
		Kind:   database.AccountKindMTProto,
		Source: database.AccountSourceDesktop,
		Name:   "Desktop",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if _, err := svc.Logout(context.Background(), row.ID); !errors.Is(err, ErrDesktopCannotLogout) {
		t.Fatalf("错误 = %v，期望 ErrDesktopCannotLogout", err)
	}
	// 账号仍然存在，只能通过 Remove 删除。
	if _, err := svc.GetByID(row.ID); err != nil {
		t.Errorf("拒绝注销不应删除账号: %v", err)
	}
	if _, _, err := svc.Remove(row.ID); err != nil {
		t.Errorf("Remove: %v", err)
	}
}

// TestSelectRequiresSession 确认切换账号前校验 session 文件。
func TestSelectRequiresSession(t *testing.T) {
	svc, _ := probeService(t)
	row, err := svc.Record(RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "No Session",
		SessionPath: filepath.Join(t.TempDir(), "missing.json"),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := svc.Select(row.ID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("错误 = %v，期望 ErrNoSession", err)
	}
	if err := svc.Select(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("错误 = %v，期望 ErrNotFound", err)
	}
}
