package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestSelectAccount 覆盖切换成功、账号不存在、session 缺失三种情况。
func TestSelectAccount(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	sessionDir := t.TempDir()
	goodPath := filepath.Join(sessionDir, "mtproto-8613800000001.json")
	if err := os.WriteFile(goodPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("写 session 文件: %v", err)
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	svc := accountsvc.New(db)
	good, err := svc.Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "Good",
		Phone:       "+8613800000001",
		SessionPath: goodPath,
	})
	if err != nil {
		t.Fatalf("Record good: %v", err)
	}
	bad, err := svc.Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "Bad",
		Phone:       "+8613800000002",
		SessionPath: filepath.Join(sessionDir, "missing.json"),
	})
	if err != nil {
		t.Fatalf("Record bad: %v", err)
	}
	_ = db.Close()

	if err := selectAccount(context.Background(), good.ID); err != nil {
		t.Fatalf("selectAccount: %v", err)
	}

	db2, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("重新打开数据库: %v", err)
	}
	current, ok, err := config.NewStore(db2).CurrentAccountID()
	_ = db2.Close()
	if err != nil {
		t.Fatalf("读取当前账号: %v", err)
	}
	if !ok || current != good.ID {
		t.Errorf("current = %d, ok = %v，期望 %d", current, ok, good.ID)
	}

	if err := selectAccount(context.Background(), 99999); !errors.Is(err, server.ErrAccountNotFound) {
		t.Errorf("不存在的账号应返回 ErrAccountNotFound，实际 %v", err)
	}
	if err := selectAccount(context.Background(), bad.ID); !errors.Is(err, server.ErrAccountNoSession) {
		t.Errorf("session 缺失应返回 ErrAccountNoSession，实际 %v", err)
	}
}
