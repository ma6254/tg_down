package mtproto

import (
	"context"
	"strings"
	"testing"
)

// TestProbeMissingSession 确认 session 不存在时给出可操作错误。
func TestProbeMissingSession(t *testing.T) {
	_, err := Probe(context.Background(), LoginOptions{
		APIID:       1,
		APIHash:     "hash",
		SessionPath: t.TempDir() + "/not-exist.json",
	})
	if err == nil {
		t.Fatal("期望报错，实际通过")
	}
	if !strings.Contains(err.Error(), "未找到 session") {
		t.Fatalf("错误信息 = %q", err.Error())
	}
}

// TestProbeRequiresCreds 确认探测仍要求 api 凭据。
func TestProbeRequiresCreds(t *testing.T) {
	_, err := Probe(context.Background(), LoginOptions{SessionPath: "x.json"})
	if err == nil || !strings.Contains(err.Error(), "api_id") {
		t.Fatalf("期望 api_id 错误，实际 %v", err)
	}
}

// TestUnixTime 确认时间戳转换与零值处理。
func TestUnixTime(t *testing.T) {
	if got := unixTime(0); !got.IsZero() {
		t.Errorf("0 应转为零值，实际 %v", got)
	}
	if got := unixTime(1700000000); got.Year() != 2023 {
		t.Errorf("unixTime(1700000000) = %v", got)
	}
}
