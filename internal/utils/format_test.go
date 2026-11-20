package utils

import "testing"

// TestFormatBytes 覆盖常用单位与边界。
func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{734003200, "700.0 MB"},
		{2 << 30, "2.0 GB"},
		{-2048, "-2.0 KB"},
	}

	for _, tt := range tests {
		if got := FormatBytes(tt.in); got != tt.want {
			t.Errorf("FormatBytes(%d) = %q，期望 %q", tt.in, got, tt.want)
		}
	}
}
