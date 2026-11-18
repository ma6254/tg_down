package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
)

// TestParseAccountID 覆盖 ID 解析与校验。
func TestParseAccountID(t *testing.T) {
	valid := map[string]int64{
		"1":    1,
		" 42 ": 42,
		"999":  999,
	}
	for raw, want := range valid {
		got, err := parseAccountID(raw)
		if err != nil {
			t.Fatalf("parseAccountID(%q) 报错: %v", raw, err)
		}
		if got != want {
			t.Errorf("parseAccountID(%q) = %d，期望 %d", raw, got, want)
		}
	}

	invalid := []string{"", "abc", "0", "-3", "1.5", "+8613800000000"}
	for _, raw := range invalid {
		if _, err := parseAccountID(raw); err == nil {
			t.Errorf("parseAccountID(%q) 应报错", raw)
		}
	}
}

// prepareSwitchEnv 准备临时数据目录、session 文件与数据库记录。
func prepareSwitchEnv(t *testing.T, withSession bool) *database.Account {
	t.Helper()
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	sessionDir := t.TempDir()
	var sessionPath string
	if withSession {
		sessionPath = filepath.Join(sessionDir, "mtproto-8613800000000.json")
		if err := os.WriteFile(sessionPath, []byte("{}"), 0o600); err != nil {
			t.Fatalf("写入 session 失败: %v", err)
		}
	}

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	account := &database.Account{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000000",
		SessionPath: sessionPath,
		State:       database.AccountStateAuthed,
	}
	if err := db.SaveAccount(account); err != nil {
		t.Fatalf("保存账号失败: %v", err)
	}
	return account
}

// currentAccountID 读取 settings 里的当前账号 ID。
func currentAccountID(t *testing.T) (int64, bool) {
	t.Helper()
	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	id, ok, err := config.NewStore(db).CurrentAccountID()
	if err != nil {
		t.Fatalf("读取当前账号失败: %v", err)
	}
	return id, ok
}

// TestSwitchByID 确认按 ID 切换会写入 settings.current_account_id。
func TestSwitchByID(t *testing.T) {
	account := prepareSwitchEnv(t, true)

	if err := runAccountSwitch(nil, []string{strconv.FormatInt(account.ID, 10)}); err != nil {
		t.Fatalf("切换失败: %v", err)
	}

	id, ok := currentAccountID(t)
	if !ok {
		t.Fatal("切换后应写入当前账号")
	}
	if id != account.ID {
		t.Errorf("current_account_id = %d，期望 %d", id, account.ID)
	}
}

// TestSwitchUnknownID 确认不存在的 ID 报错。
func TestSwitchUnknownID(t *testing.T) {
	prepareSwitchEnv(t, true)

	err := runAccountSwitch(nil, []string{"999"})
	if err == nil {
		t.Fatal("不存在的 ID 应报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestSwitchMissingSession 确认 session 文件缺失时拒绝切换。
func TestSwitchMissingSession(t *testing.T) {
	account := prepareSwitchEnv(t, false)

	err := runAccountSwitch(nil, []string{strconv.FormatInt(account.ID, 10)})
	if err == nil {
		t.Fatal("session 缺失应报错")
	}
	if !strings.Contains(err.Error(), "session 文件") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestSwitchRejectsPhone 确认不再接受手机号形式。
func TestSwitchRejectsPhone(t *testing.T) {
	prepareSwitchEnv(t, true)

	if err := runAccountSwitch(nil, []string{"+8613800000000"}); err == nil {
		t.Fatal("手机号不应被接受")
	}
}
