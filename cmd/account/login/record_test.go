package login

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
