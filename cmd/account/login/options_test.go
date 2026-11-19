package login

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

const (
	testPhone = "+8613800000000"
	testToken = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	testHash  = "0123456789abcdef0123456789abcdef"
)

// validMTProto 返回一份可通过校验的 mtproto 参数（默认走内置 desktop 应用，无需 api 凭据）。
func validMTProto() *loginOptions {
	return &loginOptions{
		Kind:       accountKindMTProto,
		App:        mtproto.AppDesktop,
		Phone:      testPhone,
		SessionDir: "./sessions",
	}
}

// TestLoginOptionsValidate 覆盖账号登录参数校验的主要分支。
func TestLoginOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    func() *loginOptions
		wantErr string // 空表示期望通过
	}{
		{
			name: "默认 mtproto 合法",
			opts: validMTProto,
		},
		{
			name: "mtproto 带验证码与密码",
			opts: func() *loginOptions {
				o := validMTProto()
				o.Code = "12345"
				o.Password = "s3cret"
				return o
			},
		},
		{
			name: "mtproto 缺手机号",
			opts: func() *loginOptions {
				o := validMTProto()
				o.Phone = ""
				return o
			},
			wantErr: "phone",
		},
		{
			name: "手机号格式非法",
			opts: func() *loginOptions {
				o := validMTProto()
				o.Phone = "13800000000"
				return o
			},
			wantErr: "E.164",
		},
		{
			name: "mtproto 误传 token",
			opts: func() *loginOptions {
				o := validMTProto()
				o.Token = testToken
				return o
			},
			wantErr: "不需要 --token",
		},
		{
			name: "custom 应用带完整凭据",
			opts: func() *loginOptions {
				o := validMTProto()
				o.App = mtproto.AppCustom
				o.APIID = 123456
				o.APIHash = testHash
				return o
			},
		},
		{
			name: "custom 应用缺凭据",
			opts: func() *loginOptions {
				o := validMTProto()
				o.App = mtproto.AppCustom
				return o
			},
			wantErr: "--app custom",
		},
		{
			name: "只提供 api_id",
			opts: func() *loginOptions {
				o := validMTProto()
				o.APIID = 123456
				return o
			},
			wantErr: "同时提供",
		},
		{
			name: "只提供 api_hash",
			opts: func() *loginOptions {
				o := validMTProto()
				o.APIHash = testHash
				return o
			},
			wantErr: "同时提供",
		},
		{
			name: "未知内置应用",
			opts: func() *loginOptions {
				o := validMTProto()
				o.App = "telegram"
				return o
			},
			wantErr: "--app",
		},
		{
			name: "mtproto session 目录为空",
			opts: func() *loginOptions {
				o := validMTProto()
				o.SessionDir = " "
				return o
			},
			wantErr: "session-dir",
		},
		{
			name: "botapi 合法",
			opts: func() *loginOptions {
				return &loginOptions{Kind: accountKindBotAPI, Token: testToken}
			},
		},
		{
			name: "botapi 缺 token",
			opts: func() *loginOptions {
				return &loginOptions{Kind: accountKindBotAPI}
			},
			wantErr: "需要 --token",
		},
		{
			name: "token 形态非法",
			opts: func() *loginOptions {
				return &loginOptions{Kind: accountKindBotAPI, Token: "not-a-token"}
			},
			wantErr: "形态不合法",
		},
		{
			name: "botapi 误传手机号",
			opts: func() *loginOptions {
				return &loginOptions{Kind: accountKindBotAPI, Token: testToken, Phone: testPhone}
			},
			wantErr: "不需要 --phone",
		},
		{
			name: "botapi 误传验证码",
			opts: func() *loginOptions {
				return &loginOptions{Kind: accountKindBotAPI, Token: testToken, Code: "12345"}
			},
			wantErr: "--code",
		},
		{
			name: "未知账号类型",
			opts: func() *loginOptions {
				return &loginOptions{Kind: "telegram", Phone: testPhone}
			},
			wantErr: "mtproto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := tt.opts()
			err := o.validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望报错包含 %q，实际通过", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestLoginOptionsBind 确认 login 的参数能正确解析。
func TestLoginOptionsBind(t *testing.T) {
	o := newLoginOptions()
	fs := pflag.NewFlagSet("login", pflag.ContinueOnError)
	o.bind(fs)

	err := fs.Parse([]string{
		"--phone", testPhone,
		"--api-id", "654321",
		"--api-hash", testHash,
		"--session-dir", "./data/sessions",
		"--name", "主号",
		"--code", "12345",
	})
	if err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}

	if o.APIID != 654321 {
		t.Errorf("APIID = %d", o.APIID)
	}
	if o.APIHash != testHash {
		t.Errorf("APIHash = %q", o.APIHash)
	}
	if o.SessionDir != "./data/sessions" {
		t.Errorf("SessionDir = %q", o.SessionDir)
	}
	if o.Name != "主号" {
		t.Errorf("Name = %q", o.Name)
	}
	if err := o.validate(); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if got := o.sessionPath(); got != filepath.Join("./data/sessions", "mtproto-8613800000000.json") {
		t.Errorf("sessionPath = %q", got)
	}
}

// TestLoginOptionsDefaultKind 确认默认走 mtproto 且环境变量可作为 api 凭据来源。
func TestLoginOptionsDefaultKind(t *testing.T) {
	t.Setenv("TG_DOWN_API_ID", "777")
	t.Setenv("TG_DOWN_API_HASH", "feedfacefeedfacefeedfacefeedface")

	o := newLoginOptions()
	if o.Kind != accountKindMTProto {
		t.Fatalf("默认 Kind = %q", o.Kind)
	}
	if o.APIID != 777 {
		t.Errorf("APIID = %d", o.APIID)
	}
	if o.APIHash == "" {
		t.Error("APIHash 未从环境变量读取")
	}
}

// TestDisplayName 确认未指定名称时按账号类型生成展示名。
func TestDisplayName(t *testing.T) {
	mtprotoOpts := validMTProto()
	if got := mtprotoOpts.displayName(); got != "+861****000" {
		t.Errorf("mtproto displayName = %q", got)
	}

	bot := &loginOptions{Kind: accountKindBotAPI, Token: testToken}
	if got := bot.displayName(); got != "bot:123456789" {
		t.Errorf("bot displayName = %q", got)
	}
}

// TestSessionPath 确认多个手机号会生成不同的 session 文件。
func TestSessionPath(t *testing.T) {
	a := &loginOptions{SessionDir: "./sessions", Phone: "+8613800000000"}
	b := &loginOptions{SessionDir: "./sessions", Phone: "+8613900000000"}
	if a.sessionPath() == b.sessionPath() {
		t.Fatalf("不同手机号生成了相同的 session 路径: %s", a.sessionPath())
	}
	if !strings.Contains(a.sessionPath(), "8613800000000") {
		t.Errorf("session 路径缺少手机号: %s", a.sessionPath())
	}
}

// TestResolveApp 确认内置 desktop 与自备凭据的选择逻辑。
func TestResolveApp(t *testing.T) {
	desktop := &loginOptions{Kind: accountKindMTProto, App: mtproto.AppDesktop}
	app, err := desktop.resolveApp()
	if err != nil {
		t.Fatalf("解析内置应用失败: %v", err)
	}
	if app.ID != 2040 {
		t.Errorf("内置 desktop 的 api_id = %d，期望 2040", app.ID)
	}

	custom := &loginOptions{Kind: accountKindMTProto, App: mtproto.AppCustom, APIID: 42, APIHash: "deadbeef"}
	app, err = custom.resolveApp()
	if err != nil {
		t.Fatalf("解析自备应用失败: %v", err)
	}
	if app.ID != 42 || app.Hash != "deadbeef" {
		t.Errorf("自备凭据 = %+v", app)
	}

	// 未指定 --app 时默认 desktop，且凭据可零配置解析。
	noApp := &loginOptions{Kind: accountKindMTProto}
	if app, err := noApp.resolveApp(); err != nil || app.ID != 2040 {
		t.Errorf("空 --app 解析结果 = %+v, %v", app, err)
	}

	if _, err := mtproto.PresetApp("nope"); err == nil {
		t.Error("未知内置应用应报错")
	}
}

// TestNewLoginOptionsDefaults 确认默认账号类型与应用。
func TestNewLoginOptionsDefaults(t *testing.T) {
	t.Setenv("TG_DOWN_API_ID", "")
	t.Setenv("TG_DOWN_API_HASH", "")

	o := newLoginOptions()
	if o.Kind != accountKindMTProto {
		t.Errorf("Kind = %q", o.Kind)
	}
	if o.App != mtproto.AppDesktop {
		t.Errorf("App = %q，期望 %q", o.App, mtproto.AppDesktop)
	}
	if o.SessionDir != "./sessions" {
		t.Errorf("SessionDir = %q", o.SessionDir)
	}
}
