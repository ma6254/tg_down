package mtproto

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionMissingAPI 确认 Session 未配应用凭据时直接报错。
func TestSessionMissingAPI(t *testing.T) {
	session := Session{SessionPath: "x.json"}
	_, err := session.Login(context.Background(), "+8613800000000", "", "")
	if err == nil {
		t.Fatal("缺少 api 凭据应报错")
	}
	if !strings.Contains(err.Error(), "api_id") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestSessionMissingFile 确认 Profile 在 session 文件不存在时报错。
func TestSessionMissingFile(t *testing.T) {
	session := Session{
		App:         App{ID: 2040, Hash: "hash"},
		SessionPath: filepath.Join(t.TempDir(), "nope.json"),
	}
	_, err := session.Profile(context.Background())
	if err == nil {
		t.Fatal("session 不存在应报错")
	}
	if !strings.Contains(err.Error(), "未找到 session 文件") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestDesktopProbeMissingPath 确认 Desktop 包装会传递路径校验错误。
func TestDesktopProbeMissingPath(t *testing.T) {
	desktop := Desktop{TDataPath: filepath.Join(t.TempDir(), "nope")}
	_, err := desktop.Probe(context.Background())
	if err == nil {
		t.Fatal("tdata 不存在应报错")
	}
	if !strings.Contains(err.Error(), "不可用") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}
