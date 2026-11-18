package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestLogoutOptionsValidate 覆盖 logout 目标解析与互斥校验。
func TestLogoutOptionsValidate(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		all        bool
		wantErr    string // 空表示期望通过
		wantID     int64
		wantName   string
		wantSummar string
	}{
		{name: "按 ID", args: []string{"3"}, wantID: 3, wantSummar: "ID 3"},
		{name: "按名称", args: []string{"my_bot"}, wantName: "my_bot", wantSummar: `名称 "my_bot"`},
		{name: "全部账号", all: true, wantSummar: "全部账号"},
		{
			name:    "全部账号又指定目标",
			args:    []string{"3"},
			all:     true,
			wantErr: "--all",
		},
		{name: "既无目标也无 all", wantErr: "请指定账号"},
		{name: "ID 非正数", args: []string{"0"}, wantErr: "正整数"},
		{name: "负 ID", args: []string{"-1"}, wantErr: "正整数"},
		{name: "空白名称", args: []string{"   "}, wantErr: "不能为空"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &logoutOptions{All: tt.all}
			err := o.validate(tt.args)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("期望通过，实际报错: %v", err)
				}
				if o.TargetID != tt.wantID {
					t.Errorf("TargetID = %d，期望 %d", o.TargetID, tt.wantID)
				}
				if o.TargetName != tt.wantName {
					t.Errorf("TargetName = %q，期望 %q", o.TargetName, tt.wantName)
				}
				if got := o.summary(); got != tt.wantSummar {
					t.Errorf("summary = %q，期望 %q", got, tt.wantSummar)
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

// TestLogoutOptionsBind 确认 --all 与 --yes 能被解析。
func TestLogoutOptionsBind(t *testing.T) {
	o := &logoutOptions{}
	fs := pflag.NewFlagSet("logout", pflag.ContinueOnError)
	o.bind(fs)

	if err := fs.Parse([]string{"--all", "--yes"}); err != nil {
		t.Fatalf("解析参数失败: %v", err)
	}
	if !o.All || !o.Yes {
		t.Fatalf("All = %v，Yes = %v", o.All, o.Yes)
	}
	if err := o.validate(nil); err != nil {
		t.Fatalf("校验失败: %v", err)
	}
}
