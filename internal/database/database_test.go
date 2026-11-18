package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// openTestDB 打开内存数据库并建表。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestAccountCRUD 覆盖账号的新增、查询、更新与删除。
func TestAccountCRUD(t *testing.T) {
	db := openTestDB(t)

	account := &Account{
		Kind:        AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000000",
		Username:    "alice",
		SessionPath: "./sessions/mtproto-8613800000000.json",
		State:       AccountStateAuthed,
	}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if account.ID == 0 {
		t.Fatal("保存后应回填 ID")
	}

	list, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 1 || list[0].Phone != account.Phone {
		t.Fatalf("列表结果异常: %+v", list)
	}

	got, err := db.GetAccount(account.ID)
	if err != nil {
		t.Fatalf("按 ID 查询失败: %v", err)
	}
	if got.Username != "alice" {
		t.Errorf("Username = %q", got.Username)
	}

	byPhone, err := db.FindAccountByPhone(account.Phone)
	if err != nil {
		t.Fatalf("按手机号查询失败: %v", err)
	}
	if byPhone.ID != account.ID {
		t.Errorf("按手机号查到的 ID = %d，期望 %d", byPhone.ID, account.ID)
	}

	// 更新状态。
	got.State = AccountStateError
	got.LastError = "session 失效"
	if err := db.SaveAccount(got); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	reloaded, err := db.GetAccount(account.ID)
	if err != nil {
		t.Fatalf("重新查询失败: %v", err)
	}
	if reloaded.State != AccountStateError || reloaded.LastError != "session 失效" {
		t.Errorf("状态未更新: %+v", reloaded)
	}

	if err := db.DeleteAccount(account.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := db.GetAccount(account.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("删除后应返回 ErrRecordNotFound，实际 %v", err)
	}
}

// TestOpenCreatesDir 确认 sqlite 文件路径的父目录会被创建。
func TestOpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	dsn := filepath.Join(dir, "tg_down.db")

	db, err := Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	defer db.Close()

	if err := db.Install(); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := os.Stat(dsn); err != nil {
		t.Fatalf("数据库文件未创建: %v", err)
	}
	if db.Driver() != "sqlite" {
		t.Errorf("Driver = %q", db.Driver())
	}
}

// TestOpenUnsupportedDriver 确认未知驱动会报错。
func TestOpenUnsupportedDriver(t *testing.T) {
	if _, err := Open("postgres", "x"); err == nil {
		t.Fatal("期望报错，实际通过")
	}
}

// TestEnsureDirMemory 确认内存库不创建目录。
func TestEnsureDirMemory(t *testing.T) {
	if err := ensureDir(":memory:"); err != nil {
		t.Errorf("内存库不应报错: %v", err)
	}
	if err := ensureDir("file::memory:?cache=shared"); err != nil {
		t.Errorf("内存库 DSN 不应报错: %v", err)
	}
}
