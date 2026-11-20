package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/appdir"
)

// openTestDB 打开内存数据库并建表。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	// session_path 的解析依赖数据目录，测试里固定到临时目录。
	t.Setenv("TG_DOWN_HOME", t.TempDir())
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

// TestAccountSessionPathRelative 确认 session_path 按相对数据目录的路径入库，
// 读出时再还原成真实文件路径。
func TestAccountSessionPathRelative(t *testing.T) {
	db := openTestDB(t)
	home, err := appdir.DataDir()
	if err != nil {
		t.Fatalf("解析数据目录失败: %v", err)
	}

	abs := filepath.Join(home, "sessions", "mtproto-8613800000001.json")
	account := &Account{
		Kind:        AccountKindMTProto,
		Name:        "相对路径",
		Phone:       "+8613800000001",
		SessionPath: abs,
		State:       AccountStateAuthed,
	}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	var stored string
	if err := db.DB.Raw("SELECT session_path FROM accounts WHERE id = ?", account.ID).
		Scan(&stored).Error; err != nil {
		t.Fatalf("读取原始值失败: %v", err)
	}
	if stored != "sessions/mtproto-8613800000001.json" {
		t.Errorf("库内 session_path = %q", stored)
	}
	// 内存里的 Account 保持真实路径，调用方不需要再解析。
	if account.SessionPath != abs {
		t.Errorf("内存路径被改写: %q", account.SessionPath)
	}

	got, err := db.GetAccount(account.ID)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if got.SessionPath != abs {
		t.Errorf("读回 session_path = %q，期望 %q", got.SessionPath, abs)
	}

	// 按绝对路径也能查到（内部会先转成相对形式）。
	found, err := db.FindAccountBySessionPath(abs)
	if err != nil {
		t.Fatalf("按 session 路径查询失败: %v", err)
	}
	if found.ID != account.ID {
		t.Errorf("查到的 ID = %d，期望 %d", found.ID, account.ID)
	}
}

// TestNormalizeSessionPaths 确认历史绝对路径会被改成相对路径，且可重复执行。
func TestNormalizeSessionPaths(t *testing.T) {
	db := openTestDB(t)
	home, err := appdir.DataDir()
	if err != nil {
		t.Fatalf("解析数据目录失败: %v", err)
	}

	abs := filepath.Join(home, "sessions", "mtproto-8613800000002.json")
	account := &Account{
		Kind:        AccountKindMTProto,
		Phone:       "+8613800000002",
		SessionPath: abs,
		State:       AccountStateAuthed,
	}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	// 模拟历史数据：绕过 SaveAccount 直接写绝对路径。
	if err := db.DB.Exec("UPDATE accounts SET session_path = ? WHERE id = ?", abs, account.ID).Error; err != nil {
		t.Fatalf("写入历史数据失败: %v", err)
	}

	if err := db.NormalizeSessionPaths(); err != nil {
		t.Fatalf("规范化失败: %v", err)
	}
	// 幂等：再次执行不应报错或改坏数据。
	if err := db.NormalizeSessionPaths(); err != nil {
		t.Fatalf("二次规范化失败: %v", err)
	}

	var stored string
	if err := db.DB.Raw("SELECT session_path FROM accounts WHERE id = ?", account.ID).
		Scan(&stored).Error; err != nil {
		t.Fatalf("读取原始值失败: %v", err)
	}
	if stored != "sessions/mtproto-8613800000002.json" {
		t.Errorf("规范化后 session_path = %q", stored)
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
