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

// TestDeleteAccount 确认删除只清理本地记录，不触发联网注销。
func TestDeleteAccount(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	row, err := accountsvc.New(db).Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Source:      database.AccountSourceDesktop,
		Name:        "Desktop Import",
		Phone:       "+8613800000003",
		SessionPath: "./sessions/desktop.json",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	_ = db.Close()

	result, err := deleteAccount(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("deleteAccount: %v", err)
	}
	if !result.OK {
		t.Error("OK 应为 true")
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

// TestDeleteAccountNotFound 确认不存在的账号返回可映射错误。
func TestDeleteAccountNotFound(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	if _, err := deleteAccount(context.Background(), 999); !errors.Is(err, server.ErrAccountNotFound) {
		t.Errorf("err = %v，期望 ErrAccountNotFound", err)
	}
}
