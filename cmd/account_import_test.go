package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestImportDesktopAccounts 确认探测结果会写入 session 文件与数据库，
// 且重复导入不会覆盖已有 session。
func TestImportDesktopAccounts(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	sessionDir := t.TempDir()
	lo := &loginOptions{
		Kind:       accountKindMTProto,
		SessionDir: sessionDir,
	}

	result := &mtproto.DesktopProbeResult{
		TDataPath: `C:\tdata`,
		Accounts: []mtproto.DesktopAccount{
			{
				Account: &mtproto.Account{
					ID:        42,
					Phone:     "+8613800000000",
					Username:  "alice",
					FirstName: "Alice",
				},
				Session: &session.Data{DC: 2, AuthKey: make([]byte, 256)},
			},
			{Err: os.ErrNotExist}, // 失败的账号应被忽略
		},
	}

	imported, skipped, err := importDesktopAccounts(lo, result)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported=%d skipped=%d，期望 1/0", imported, skipped)
	}

	sessionPath := filepath.Join(sessionDir, "mtproto-8613800000000.json")
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session 文件未生成: %v", err)
	}

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	accounts, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("应有 1 个账号，实际 %d", len(accounts))
	}
	if accounts[0].Username != "alice" || accounts[0].SessionPath != sessionPath {
		t.Errorf("账号记录异常: %+v", accounts[0])
	}
	db.Close()

	// 再次导入：session 已存在，保留原文件但仍更新记录。
	imported, skipped, err = importDesktopAccounts(lo, result)
	if err != nil {
		t.Fatalf("第二次导入失败: %v", err)
	}
	if imported != 0 || skipped != 1 {
		t.Fatalf("imported=%d skipped=%d，期望 0/1", imported, skipped)
	}

	db, err = openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	accounts, err = db.ListAccounts()
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("重复导入不应新增账号，实际 %d", len(accounts))
	}
}

// TestImportDesktopAccountsMissingSession 确认没有 session 数据时不会写入文件。
func TestImportDesktopAccountsMissingSession(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	lo := &loginOptions{Kind: accountKindMTProto, SessionDir: t.TempDir()}
	result := &mtproto.DesktopProbeResult{
		Accounts: []mtproto.DesktopAccount{
			{
				Account: &mtproto.Account{ID: 1, Phone: "+8613900000000"},
				Session: nil,
			},
		},
	}

	if _, _, err := importDesktopAccounts(lo, result); err == nil {
		t.Fatal("缺少 session 数据应报错")
	}
}
