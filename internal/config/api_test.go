package config

import (
	"strings"
	"testing"
)

// TestValidateAPIID 覆盖 api_id 的合法与非法输入。
func TestValidateAPIID(t *testing.T) {
	valid := []string{"1", "2040", "1234567890", " 123456 "}
	for _, value := range valid {
		if err := ValidateAPIID(value); err != nil {
			t.Errorf("ValidateAPIID(%q) 报错: %v", value, err)
		}
	}

	invalid := []string{"", "abc", "0", "-1", "2147483648"}
	for _, value := range invalid {
		if err := ValidateAPIID(value); err == nil {
			t.Errorf("ValidateAPIID(%q) 期望报错，实际通过", value)
		}
	}
}

// TestValidateAPIHash 覆盖 api_hash 的格式校验。
func TestValidateAPIHash(t *testing.T) {
	valid := []string{
		strings.Repeat("a", 32),
		"0123456789abcdef0123456789ABCDEF",
		"  " + strings.Repeat("0", 32) + "  ",
	}
	for _, value := range valid {
		if err := ValidateAPIHash(value); err != nil {
			t.Errorf("ValidateAPIHash(%q) 报错: %v", value, err)
		}
	}

	invalid := []string{
		"",
		strings.Repeat("a", 31),
		strings.Repeat("a", 33),
		strings.Repeat("z", 32),
		"not-a-hex-string",
	}
	for _, value := range invalid {
		if err := ValidateAPIHash(value); err == nil {
			t.Errorf("ValidateAPIHash(%q) 期望报错，实际通过", value)
		}
	}
}

// TestAPIItemsRegistered 确认新键登记在 items 里且带校验。
func TestAPIItemsRegistered(t *testing.T) {
	tests := []struct {
		key      string
		wantType string
	}{
		{KeyAPIID, TypeInteger},
		{KeyAPIHash, TypeString},
		{KeyAPICustomEnabled, TypeBoolean},
		{KeyWebTheme, TypeString},
		{KeyWebTitleSpeed, TypeBoolean},
	}

	for _, tt := range tests {
		item, ok := Lookup(tt.key)
		if !ok {
			t.Fatalf("配置项 %s 未登记", tt.key)
		}
		if item.Type != tt.wantType {
			t.Errorf("%s 类型 = %q，期望 %q", tt.key, item.Type, tt.wantType)
		}
		if item.Validate == nil {
			t.Errorf("%s 缺少校验函数", tt.key)
		}
		if item.Description == "" {
			t.Errorf("%s 缺少描述", tt.key)
		}
	}
}
