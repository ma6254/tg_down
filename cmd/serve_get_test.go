package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestGetAccount 覆盖详情字段、当前账号标记与 session_present。
func TestGetAccount(t *testing.T) {
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
		Username:    "tester",
		SessionPath: goodPath,
	})
	if err != nil {
		t.Fatalf("Record good: %v", err)
	}
	if err := svc.SetCurrentAccountID(good.ID); err != nil {
		t.Fatalf("SetCurrentAccountID: %v", err)
	}
	missing, err := svc.Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "Missing",
		Phone:       "+8613800000002",
		SessionPath: filepath.Join(sessionDir, "missing.json"),
	})
	if err != nil {
		t.Fatalf("Record missing: %v", err)
	}
	_ = db.Close()

	detail, err := getAccount(context.Background(), good.ID)
	if err != nil {
		t.Fatalf("getAccount: %v", err)
	}
	if detail.ID != good.ID || detail.Name != "Good" || detail.Username != "tester" {
		t.Errorf("详情 = %+v", detail)
	}
	if !detail.Current {
		t.Error("应标记为当前账号")
	}
	if !detail.SessionPresent {
		t.Error("session 文件存在时 session_present 应为 true")
	}
	if detail.CreatedAt == 0 {
		t.Error("created_at 未填充")
	}

	// session 文件缺失的账号仍能查到，但 session_present 为 false。
	missingDetail, err := getAccount(context.Background(), missing.ID)
	if err != nil {
		t.Fatalf("getAccount missing: %v", err)
	}
	if missingDetail.SessionPresent {
		t.Error("session 文件缺失时 session_present 应为 false")
	}

	if _, err := getAccount(context.Background(), 99999); !errors.Is(err, server.ErrAccountNotFound) {
		t.Errorf("不存在的账号应返回 ErrAccountNotFound，实际 %v", err)
	}
}
