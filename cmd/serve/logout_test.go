package serve

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestLogoutAccountLocalOnly 确认没有 session 文件时也能清掉本地账号。
func TestLogoutAccountLocalOnly(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	row, err := accountsvc.New(db).Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "Test User",
		Phone:       "+8613800000001",
		SessionPath: "./sessions/missing.json",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	_ = db.Close()

	result, err := logoutAccount(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("logoutAccount: %v", err)
	}
	if !result.OK {
		t.Error("OK 应为 true")
	}
	if result.Revoked {
		t.Error("没有 session 文件时不应报已吊销")
	}
	if result.RemovedSession {
		t.Error("文件本就不存在，不应报已清理")
	}

	db, err = utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.GetAccount(row.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("账号仍存在: %v", err)
	}
}

// TestLogoutAccountNotFound 确认不存在的账号返回可映射的错误。
func TestLogoutAccountNotFound(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	if _, err := logoutAccount(context.Background(), 999); !errors.Is(err, server.ErrAccountNotFound) {
		t.Errorf("err = %v，期望 ErrAccountNotFound", err)
	}
}
