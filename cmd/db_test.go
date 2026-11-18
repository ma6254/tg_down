package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestRecordAccount 确认账号会写入数据库，且重复登录按 session 路径更新而不是新增。
func TestRecordAccount(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	session := filepath.Join(t.TempDir(), "mtproto-8613800000000.json")
	if err := os.WriteFile(session, []byte("{}"), 0o600); err != nil {
		t.Fatalf("准备 session 失败: %v", err)
	}

	lo := &loginOptions{Kind: accountKindMTProto, Name: "主号"}
	account := &mtproto.Account{ID: 42, Phone: "+8613800000000", Username: "alice", FirstName: "Alice"}

	if _, err := recordAccount(lo, account, session); err != nil {
		t.Fatalf("写入账号失败: %v", err)
	}

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	list, err := db.ListAccounts()
	if err != nil {
		t.Fatalf("读取列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 个账号，实际 %d", len(list))
	}
	if list[0].Name != "主号" || list[0].Username != "alice" || list[0].State != "authed" {
		t.Errorf("账号字段异常: %+v", list[0])
	}

	// 再次登记：同 session 路径应更新而非新增。
	account.Username = "alice2"
	if _, err := recordAccount(lo, account, session); err != nil {
		t.Fatalf("再次写入失败: %v", err)
	}
	list, err = db.ListAccounts()
	if err != nil {
		t.Fatalf("读取列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("重复登录不应新增账号，实际 %d 个", len(list))
	}
	if list[0].Username != "alice2" {
		t.Errorf("用户名未更新: %+v", list[0])
	}
}

// TestFindLogoutTargets 确认按 ID / 手机号 / 名称查找待删除账号。
func TestFindLogoutTargets(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	account := &database.Account{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000000",
		Username:    "alice",
		SessionPath: "./sessions/mtproto-8613800000000.json",
		State:       database.AccountStateAuthed,
	}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("保存账号失败: %v", err)
	}

	saved := logoutOpts
	defer func() { logoutOpts = saved }()

	// 按 ID。
	logoutOpts = &logoutOptions{TargetID: account.ID}
	targets, err := findLogoutTargets(db)
	if err != nil {
		t.Fatalf("按 ID 查找失败: %v", err)
	}
	if len(targets) != 1 || targets[0].ID != account.ID {
		t.Fatalf("按 ID 查找结果异常: %+v", targets)
	}

	// 按手机号（带加号）。
	logoutOpts = &logoutOptions{TargetName: "+8613800000000"}
	if targets, err = findLogoutTargets(db); err != nil || len(targets) != 1 {
		t.Fatalf("按手机号查找失败: %v, %+v", err, targets)
	}

	// 按名称。
	logoutOpts = &logoutOptions{TargetName: "主号"}
	if targets, err = findLogoutTargets(db); err != nil || len(targets) != 1 {
		t.Fatalf("按名称查找失败: %v, %+v", err, targets)
	}

	// 不存在。
	logoutOpts = &logoutOptions{TargetName: "nobody"}
	if _, err = findLogoutTargets(db); err == nil {
		t.Fatal("不存在的账号应报错")
	}

	// 全部。
	logoutOpts = &logoutOptions{All: true}
	if targets, err = findLogoutTargets(db); err != nil || len(targets) != 1 {
		t.Fatalf("--all 查找失败: %v, %+v", err, targets)
	}
}

// TestAutoSelectSingleAccount 确认只有唯一账号时自动设为当前账号。
func TestAutoSelectSingleAccount(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	first := &database.Account{Kind: database.AccountKindMTProto, Name: "主号", SessionPath: "a.json", State: database.AccountStateAuthed}
	if err := db.SaveAccount(first); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	selected, err := autoSelectSingleAccount()
	if err != nil {
		t.Fatalf("自动选中失败: %v", err)
	}
	if !selected {
		t.Fatal("唯一账号应被自动选中")
	}
	if id, ok, _ := config.NewStore(db).CurrentAccountID(); !ok || id != first.ID {
		t.Errorf("current_account_id = %d ok=%v，期望 %d", id, ok, first.ID)
	}

	// 再次调用：已经是当前账号，不应重复"选中"。
	if selected, err := autoSelectSingleAccount(); err != nil || selected {
		t.Errorf("已是当前账号时 selected=%v err=%v", selected, err)
	}

	// 加入第二个账号后不再自动切换。
	second := &database.Account{Kind: database.AccountKindMTProto, Name: "小号", SessionPath: "b.json"}
	if err := db.SaveAccount(second); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if selected, err := autoSelectSingleAccount(); err != nil || selected {
		t.Errorf("多个账号时不应自动选中，selected=%v err=%v", selected, err)
	}
}
