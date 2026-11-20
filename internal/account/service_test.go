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
				Account: &mtproto.Account{ID: 555, Phone: "+8613800000000", Username: "alice", FirstName: "Alice"},
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
	rows, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].TelegramIDValue() != 555 {
		t.Fatalf("导入后账号 = %+v，期望 TelegramID 555", rows)
	}
	if rows[0].Source != database.AccountSourceDesktop {
		t.Errorf("导入来源 = %q，期望 desktop", rows[0].Source)
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

// TestRecordByTelegramID 确认换绑手机号（session 路径变化）仍复用同一账号。
func TestRecordByTelegramID(t *testing.T) {
	svc := newTestService(t)

	first, err := svc.Record(RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000001",
		TelegramID:  777,
		SessionPath: "./sessions/old.json",
	})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}

	second, err := svc.Record(RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Phone:       "+8613800000002",
		TelegramID:  777,
		SessionPath: "./sessions/new.json",
	})
	if err != nil {
		t.Fatalf("再次登记失败: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("应按 TelegramID 复用记录：%d != %d", second.ID, first.ID)
	}
	if second.SessionPath != "./sessions/new.json" {
		t.Errorf("SessionPath 未跟随最新登录: %q", second.SessionPath)
	}
	if second.TelegramIDValue() != 777 {
		t.Errorf("TelegramID = %d，期望 777", second.TelegramIDValue())
	}

	list, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("账号数量 = %d，期望 1", len(list))
	}
}

// TestRecordSource 确认来源会写入，且后续未提供来源的登记不会把它清空。
func TestRecordSource(t *testing.T) {
	svc := newTestService(t)

	in := RecordInput{
		Kind:        database.AccountKindMTProto,
		Source:      database.AccountSourceManual,
		Name:        "主号",
		SessionPath: "./sessions/a.json",
	}
	row, err := svc.Record(in)
	if err != nil {
		t.Fatal(err)
	}
	if row.Source != database.AccountSourceManual {
		t.Fatalf("Source = %q", row.Source)
	}

	in.Source = ""
	row, err = svc.Record(in)
	if err != nil {
		t.Fatal(err)
	}
	if row.Source != database.AccountSourceManual {
		t.Errorf("未提供来源时不应清空，实际 %q", row.Source)
	}

	in.Source = database.AccountSourceDesktop
	row, err = svc.Record(in)
	if err != nil {
		t.Fatal(err)
	}
	if row.Source != database.AccountSourceDesktop {
		t.Errorf("Source = %q，期望 desktop", row.Source)
	}
}
// TestBindTelegramID 确认联网拿到的 user.id 会补写到账号记录。
func TestBindTelegramID(t *testing.T) {
	svc := newTestService(t)

	row, err := svc.Record(RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		SessionPath: "./sessions/a.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.TelegramIDValue() != 0 {
		t.Fatal("初始不应有 TelegramID")
	}

	if err := svc.BindTelegramID(row.ID, 888); err != nil {
		t.Fatalf("绑定失败: %v", err)
	}
	got, err := svc.db.GetAccount(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TelegramIDValue() != 888 {
		t.Errorf("TelegramID = %d，期望 888", got.TelegramIDValue())
	}

	// 已被别的账号占用时不覆盖。
	other, err := svc.Record(RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        "另一个",
		SessionPath: "./sessions/b.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BindTelegramID(other.ID, 888); err != nil {
		t.Fatalf("冲突绑定不应报错: %v", err)
	}
	other, err = svc.db.GetAccount(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.TelegramIDValue() != 0 {
		t.Errorf("冲突时不应写入，实际 %d", other.TelegramIDValue())
	}
}
