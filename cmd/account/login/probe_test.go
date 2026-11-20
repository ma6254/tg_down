package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestProbeValidate 覆盖 --probe 模式的参数校验。
func TestProbeValidate(t *testing.T) {
	tests := []struct {
		name    string
		opts    *loginOptions
		wantErr string
	}{
		{
			name: "probe 无需手机号",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true,
				APIID: 123456, APIHash: testHash,
			},
		},
		{
			name: "probe 带手机号",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true,
				APIID: 123456, APIHash: testHash,
				Phone: testPhone,
			},
		},
		{
			name: "probe 带验证码",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true,
				APIID: 123456, APIHash: testHash,
				Code: "12345",
			},
			wantErr: "--probe",
		},
		{
			name: "probe 不支持 botapi",
			opts: &loginOptions{
				Kind: accountKindBotAPI, Probe: true, Token: testToken,
			},
			wantErr: "--probe",
		},
		{
			name: "probe 默认使用内置 desktop，无需 api 凭据",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true,
			},
		},
		{
			name: "probe 忽略应用与 api 凭据",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true, App: mtproto.AppCustom,
			},
		},
		{
			name: "probe 不使用手机号",
			opts: &loginOptions{
				Kind: accountKindMTProto, Probe: true,
				APIID: 123456, APIHash: testHash,
				Phone: "13800000000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
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

// TestResolveSessionPath 覆盖 session 路径的推导规则：只能落在数据目录下。
func TestResolveSessionPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TG_DOWN_HOME", home)
	sessions := filepath.Join(home, "sessions")

	t.Run("按手机号推导", func(t *testing.T) {
		o := &loginOptions{Phone: testPhone}
		got, err := o.resolveSessionPath()
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		want := filepath.Join(sessions, "mtproto-8613800000000.json")
		if got != want {
			t.Errorf("got %q，期望 %q", got, want)
		}
	})

	t.Run("目录为空时报错", func(t *testing.T) {
		o := &loginOptions{}
		if _, err := o.resolveSessionPath(); err == nil {
			t.Fatal("期望报错，实际通过")
		}
	})

	t.Run("目录中唯一 session 可直接使用", func(t *testing.T) {
		if err := os.MkdirAll(sessions, 0o700); err != nil {
			t.Fatalf("创建目录失败: %v", err)
		}
		path := filepath.Join(sessions, "mtproto-8613800000000.json")
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		o := &loginOptions{}
		got, err := o.resolveSessionPath()
		if err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		if got != path {
			t.Errorf("got %q，期望 %q", got, path)
		}
	})

	t.Run("多个 session 需要 --phone", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(sessions, "mtproto-8613900000000.json"), []byte("{}"), 0o600); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		o := &loginOptions{}
		if _, err := o.resolveSessionPath(); err == nil {
			t.Fatal("期望报错，实际通过")
		}
	})
}
