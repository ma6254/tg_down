package serve

import (
	"context"
	"errors"
	"testing"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestListAccountsEmpty 确认空库返回空切片而不是错误。
func TestListAccountsEmpty(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	list, err := listAccounts(context.Background())
	if err != nil {
		t.Fatalf("listAccounts: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空库应返回空列表，实际 %+v", list)
	}
}

// TestListAccounts 确认库里的账号与当前选中标记被正确映射。
func TestListAccounts(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	svc := accountsvc.New(db)
	row, err := svc.Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "Test User",
		Phone:       "+8613800000001",
		Username:    "tester",
		SessionPath: "./sessions/mtproto-8613800000001.json",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := svc.SetCurrentAccountID(row.ID); err != nil {
		t.Fatalf("SetCurrentAccountID: %v", err)
	}
	_ = db.Close()

	list, err := listAccounts(context.Background())
	if err != nil {
		t.Fatalf("listAccounts: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("账号数 = %d，期望 1", len(list))
	}

	got := list[0]
	if got.ID != row.ID {
		t.Errorf("id = %d，期望 %d", got.ID, row.ID)
	}
	if got.Name != "Test User" || got.Username != "tester" || got.Phone != "+8613800000001" {
		t.Errorf("账号字段 = %+v", got)
	}
	if got.State != database.AccountStateAuthed {
		t.Errorf("state = %q，期望 authed", got.State)
	}
	if !got.Current {
		t.Error("current 未标记为当前账号")
	}
	if got.CreatedAt == 0 {
		t.Error("created_at 未填充")
	}
}

// TestGetAccountLiveNotFound 确认不存在的账号直接返回 ErrAccountNotFound。
func TestGetAccountLiveNotFound(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	if _, err := getAccountLive(context.Background(), 99); !errors.Is(err, server.ErrAccountNotFound) {
		t.Fatalf("err = %v，期望 ErrAccountNotFound", err)
	}
}

// TestGetAccountLiveNoSession 确认 session 缺失时在联网前就返回 ErrAccountNoSession。
func TestGetAccountLiveNoSession(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	row, err := accountsvc.New(db).Record(accountsvc.RecordInput{
		Kind: database.AccountKindMTProto,
		Name: "No Session",
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("Record: %v", err)
	}
	_ = db.Close()

	if _, err := getAccountLive(context.Background(), row.ID); !errors.Is(err, server.ErrAccountNoSession) {
		t.Fatalf("err = %v，期望 ErrAccountNoSession", err)
	}
}
