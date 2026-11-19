package accountlist

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
)

// TestPrintAccounts 确认账号表包含关键信息并用 * 标出当前账号。
func TestPrintAccounts(t *testing.T) {
	accounts := []database.Account{
		{
			ID:          1,
			Kind:        database.AccountKindMTProto,
			Name:        "主号",
			Phone:       "+8613800000000",
			State:       database.AccountStateAuthed,
			SessionPath: "./sessions/mtproto-8613800000000.json",
		},
		{
			ID:          2,
			Kind:        database.AccountKindMTProto,
			Phone:       "+8613900000000",
			State:       database.AccountStateError,
			SessionPath: "./sessions/mtproto-8613900000000.json",
		},
	}

	var buf bytes.Buffer
	printAccounts(&buf, accounts, 1)
	out := buf.String()

	for _, want := range []string{"本地账号（2）", "主号", "+861****000", "authed", "*"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q：\n%s", want, out)
		}
	}
}

// TestPrintAccountsEmpty 确认空列表给出提示。
func TestPrintAccountsEmpty(t *testing.T) {
	var buf bytes.Buffer
	printAccounts(&buf, nil, 0)
	if !strings.Contains(buf.String(), "无（执行 account login 后出现）") {
		t.Errorf("空列表提示缺失：%s", buf.String())
	}
}
