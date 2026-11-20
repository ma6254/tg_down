package mtproto

import (
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"
)

// TestFriendlyError 覆盖常见 RPC 错误的翻译。
func TestFriendlyError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"验证码错误", tgerr.New(400, "PHONE_CODE_INVALID"), "验证码错误"},
		{"验证码过期", tgerr.New(400, "PHONE_CODE_EXPIRED"), "过期"},
		{"密码错误", tgerr.New(400, "PASSWORD_HASH_INVALID"), "两步验证密码错误"},
		{"手机号无效", tgerr.New(400, "PHONE_NUMBER_INVALID"), "手机号无效"},
		{"api 凭据错误", tgerr.New(400, "API_ID_INVALID"), "api_id"},
		{"限流", tgerr.New(420, "FLOOD_WAIT_30"), "限流"},
		{"频道不可访问", tgerr.New(400, "CHANNEL_PRIVATE"), "无法访问该频道"},
		{"用户名不存在", tgerr.New(400, "USERNAME_NOT_OCCUPIED"), "用户名不存在"},
		{"未识别的错误保留类型", tgerr.New(400, "SOMETHING_ELSE"), "SOMETHING_ELSE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := friendlyError(tt.err)
			if got == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !strings.Contains(got.Error(), tt.want) {
				t.Fatalf("错误信息 %q 未包含 %q", got.Error(), tt.want)
			}
		})
	}

	if err := friendlyError(nil); err != nil {
		t.Fatalf("nil 应返回 nil，实际 %v", err)
	}
}

// TestMaskPhone 确认手机号打码规则。
func TestMaskPhone(t *testing.T) {
	if got := maskPhone("+8613800000000"); got != "+861****000" {
		t.Errorf("maskPhone = %q", got)
	}
	// 过短的输入原样返回，避免越界。
	if got := maskPhone("+861"); got != "+861" {
		t.Errorf("短号码 maskPhone = %q", got)
	}
}
