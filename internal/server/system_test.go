package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"testing"

	"github.com/ma6254/tg_down/internal/build"
)

// TestSystemInfo 确认返回编译期注入的版本信息与运行环境。
func TestSystemInfo(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/system", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cc)
	}

	var info SystemInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != build.Version {
		t.Errorf("version = %q，期望 %q", info.Version, build.Version)
	}
	if info.Commit != build.Commit {
		t.Errorf("commit = %q，期望 %q", info.Commit, build.Commit)
	}
	if info.BuildTime != build.BuildTime {
		t.Errorf("build_time = %q，期望 %q", info.BuildTime, build.BuildTime)
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("go_version = %q，期望 %q", info.GoVersion, runtime.Version())
	}
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH {
		t.Errorf("运行环境 = %s/%s，期望 %s/%s", info.OS, info.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if info.UptimeSeconds < 0 {
		t.Errorf("uptime_seconds = %d，不应为负数", info.UptimeSeconds)
	}
}
