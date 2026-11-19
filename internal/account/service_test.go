package account

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/session"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// newTestService 创建内存数据库上的账号服务。
func newTestService(t *testing.T) *Service {
	t.Helper()

	db, err := database.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

// TestRecordDedupe 确认同一 session 路径重复登记是更新而不是新增。
func TestRecordDedupe(t *testing.T) {
	svc := newTestService(t)

	in := RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000000",
		Username:    "alice",
		SessionPath: "./sessions/a.json",
	}
	first, err := svc.Record(in)
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	if first.ID == 0 {
		t.Fatal("应回填 ID")
	}

	in.Username = "alice2"
	second, err := svc.Record(in)
	if err != nil {
		t.Fatalf("再次登记失败: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("应复用同一记录，ID %d != %d", second.ID, first.ID)
	}

	list, err := svc.List()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 1 || list[0].Username != "alice2" {
		t.Fatalf("记录异常: %+v", list)
	}
}

// TestAutoSelectSingle 确认唯一账号会自动成为当前账号。
func TestAutoSelectSingle(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	svc := newTestService(t)

	first, err := svc.Record(RecordInput{Kind: database.AccountKindMTProto, SessionPath: "a.json"})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}

	selected, err := svc.AutoSelectSingle()
	if err != nil || !selected {
		t.Fatalf("唯一账号应自动选中: selected=%v err=%v", selected, err)
	}
	if id, ok, _ := config.NewStore(svc.db).CurrentAccountID(); !ok || id != first.ID {
		t.Errorf("current_account_id = %d ok=%v", id, ok)
	}

	// 第二个账号加入后不再自动切换。
	if _, err := svc.Record(RecordInput{Kind: database.AccountKindMTProto, SessionPath: "b.json"}); err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	if selected, err := svc.AutoSelectSingle(); err != nil || selected {
		t.Errorf("多账号不应自动选中: selected=%v err=%v", selected, err)
	}
}

// TestDeleteRemovesSessionAndCurrent 确认删除会清理 session 文件与当前账号。
func TestDeleteRemovesSessionAndCurrent(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	svc := newTestService(t)

	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "mtproto-8613800000000.json")
	if err := os.WriteFile(sessionPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("写入 session 失败: %v", err)
	}

	row, err := svc.Record(RecordInput{Kind: database.AccountKindMTProto, SessionPath: sessionPath})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	if _, err := svc.AutoSelectSingle(); err != nil {
		t.Fatalf("自动选中失败: %v", err)
	}

	removed, err := svc.Delete(*row)
	if err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if !removed {
		t.Error("应删除 session 文件")
	}
	if _, err := os.Stat(sessionPath); !os.IsNotExist(err) {
		t.Errorf("session 文件仍存在: %v", err)
	}
	if _, ok, _ := config.NewStore(svc.db).CurrentAccountID(); ok {
		t.Error("当前账号应被清空")
	}
}

// TestSelectForLogout 覆盖按 ID / 手机号 / 名称 / 全部查找。
func TestSelectForLogout(t *testing.T) {
	svc := newTestService(t)

	row, err := svc.Record(RecordInput{
		Kind: database.AccountKindMTProto, Name: "主号",
		Phone: "+8613800000000", Username: "alice", SessionPath: "a.json",
	})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}

	tests := []struct {
		name string
		id   int64
		key  string
		all  bool
	}{
		{name: "按 ID", id: row.ID},
		{name: "按手机号", key: "+8613800000000"},
		{name: "按名称", key: "主号"},
		{name: "按用户名", key: "alice"},
		{name: "全部", all: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := svc.SelectForLogout(tt.id, tt.key, tt.all)
			if err != nil {
				t.Fatalf("查找失败: %v", err)
			}
			if len(targets) != 1 || targets[0].ID != row.ID {
				t.Fatalf("结果异常: %+v", targets)
			}
		})
	}

	if _, err := svc.SelectForLogout(999, "", false); err == nil {
		t.Error("不存在的 ID 应报错")
	}
}

// TestImportDesktop 确认导入会写 session 文件并登记账号。
func TestImportDesktop(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	svc := newTestService(t)

	sessionDir := t.TempDir()
	result := &mtproto.DesktopProbeResult{
		Accounts: []mtproto.DesktopAccount{
			{
				Account: &mtproto.Account{Phone: "+8613800000000", Username: "alice", FirstName: "Alice"},
				Session: &session.Data{DC: 2, AuthKey: make([]byte, 256)},
			},
		},
	}

	out, err := svc.ImportDesktop(sessionDir, result)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if out.Imported != 1 || out.Skipped != 0 {
		t.Fatalf("结果异常: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "mtproto-8613800000000.json")); err != nil {
		t.Fatalf("session 文件未生成: %v", err)
	}

	// 再导入一次：文件已存在，只更新记录。
	out, err = svc.ImportDesktop(sessionDir, result)
	if err != nil {
		t.Fatalf("第二次导入失败: %v", err)
	}
	if out.Imported != 0 || out.Skipped != 1 {
		t.Fatalf("第二次结果异常: %+v", out)
	}
}
