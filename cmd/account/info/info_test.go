package accountinfo

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestPrintAccountInfo 确认信息表包含关键字段并标出当前账号。
func TestPrintAccountInfo(t *testing.T) {
	account := &database.Account{
		ID:          7,
		Kind:        database.AccountKindMTProto,
		Name:        "主号",
		Username:    "alice",
		Phone:       "+8613800000000",
		SessionPath: "./sessions/mtproto-8613800000000.json",
		State:       database.AccountStateAuthed,
		CreatedAt:   time.Date(2026, 11, 18, 23, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 11, 18, 23, 10, 0, 0, time.UTC),
	}

	var buf bytes.Buffer
	printAccountInfo(&buf, account, true, nil)
	out := buf.String()

	for _, want := range []string{
		"（当前账号）", "7", "mtproto", "主号", "@alice", "+861****000",
		"authed", "mtproto-8613800000000.json", "2026-11-18 23:00", "2026-11-18 23:10",
		"会员", "未知（加 --live 查询）",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "+8613800000000") {
		t.Errorf("手机号未脱敏：\n%s", out)
	}
}

// TestPrintAccountInfoMissingFields 确认空字段显示为占位符。
func TestPrintAccountInfoMissingFields(t *testing.T) {
	account := &database.Account{ID: 1, Kind: database.AccountKindMTProto}

	var buf bytes.Buffer
	printAccountInfo(&buf, account, false, nil)
	out := buf.String()

	if strings.Contains(out, "（当前账号）") {
		t.Errorf("非当前账号不应带标记：\n%s", out)
	}
	if !strings.Contains(out, "未知") {
		t.Errorf("创建时间应为未知：\n%s", out)
	}
}

// TestResolveInfoAccountID 确认 ID 解析优先级：参数 > 当前账号。
func TestResolveInfoAccountID(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	// 既没有参数也没有当前账号。
	if _, err := resolveInfoAccountID(db, nil); err == nil {
		t.Fatal("无参数且无当前账号应报错")
	}

	if err := config.NewStore(db).SetCurrentAccountID(5); err != nil {
		t.Fatalf("设置当前账号失败: %v", err)
	}
	id, err := resolveInfoAccountID(db, nil)
	if err != nil {
		t.Fatalf("读取当前账号失败: %v", err)
	}
	if id != 5 {
		t.Errorf("id = %d，期望 5", id)
	}

	// 参数优先。
	id, err = resolveInfoAccountID(db, []string{"9"})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if id != 9 {
		t.Errorf("id = %d，期望 9", id)
	}
}

// TestPrintLiveInfo 覆盖在线资料的账号、生日、简介与设备列表。
func TestPrintLiveInfo(t *testing.T) {
	tests := []struct {
		name string
		info *mtproto.LiveInfo
		want []string
		not  []string
	}{
		{
			name: "完整资料",
			info: &mtproto.LiveInfo{
				Account: &mtproto.Account{
					ID: 42, Username: "alice", FirstName: "Alice",
					Phone: "+8613800000000", Premium: true,
				},
				Profile: &mtproto.ProfileInfo{
					HasBirthday: true, BirthDay: 12, BirthMonth: 5, BirthYear: 1990,
					About: "hello",
				},
				Devices: []mtproto.Device{
					{AppName: "Telegram Desktop", Platform: "PC", Current: true},
				},
			},
			want: []string{"Alice", "@alice", "+861****000", "1990-05-12", "hello", "已登录客户端（1）", "Telegram Desktop"},
			not:  []string{"+8613800000000"},
		},
		{
			name: "未填年份",
			info: &mtproto.LiveInfo{
				Profile: &mtproto.ProfileInfo{HasBirthday: true, BirthDay: 12, BirthMonth: 5},
			},
			want: []string{"5 月 12 日（未填年份）"},
		},
		{
			name: "未公开",
			info: &mtproto.LiveInfo{},
			want: []string{"未公开", "简介"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printLiveInfo(&buf, tt.info)
			out := buf.String()

			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("输出缺少 %q：\n%s", want, out)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(out, not) {
					t.Errorf("输出不应包含 %q：\n%s", not, out)
				}
			}
		})
	}
}

// TestPrintAccountInfoMember 确认会员行在离线/在线时的取值。
func TestPrintAccountInfoMember(t *testing.T) {
	account := &database.Account{ID: 1, Kind: database.AccountKindMTProto}

	tests := []struct {
		name  string
		live  *mtproto.LiveInfo
		wants string
	}{
		{name: "离线未知", live: nil, wants: "未知"},
		{name: "会员", live: &mtproto.LiveInfo{Account: &mtproto.Account{Premium: true}}, wants: "是"},
		{name: "非会员", live: &mtproto.LiveInfo{Account: &mtproto.Account{Premium: false}}, wants: "否"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printAccountInfo(&buf, account, false, tt.live)

			for _, line := range strings.Split(buf.String(), "\n") {
				if !strings.Contains(line, "会员") {
					continue
				}
				if !strings.Contains(line, tt.wants) {
					t.Errorf("会员行 %q 未包含 %q", line, tt.wants)
				}
				return
			}
			t.Fatal("输出缺少会员行")
		})
	}
}
