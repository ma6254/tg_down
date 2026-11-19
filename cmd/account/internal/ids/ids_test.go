package ids

import "testing"

// TestParse 覆盖合法与非法的账号 ID。
func TestParse(t *testing.T) {
	valid := map[string]int64{"1": 1, " 42 ": 42}
	for raw, want := range valid {
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) 报错: %v", raw, err)
		}
		if got != want {
			t.Errorf("Parse(%q) = %d，期望 %d", raw, got, want)
		}
	}

	invalid := []string{"", "abc", "0", "-1", "+8613800000000", "1.5"}
	for _, raw := range invalid {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) 应报错", raw)
		}
	}
}
