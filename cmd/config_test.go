package cmd

import (
	"testing"

	"github.com/ma6254/tg_down/internal/config"
)

// TestResolveProxy 确认代理只从配置表读取，未配置时返回空。
func TestResolveProxy(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	got, err := resolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}

	db, err := openDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := config.NewStore(db).Set(config.KeyHTTPProxy, "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	db.Close()

	got, err = resolveProxy()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != "http://127.0.0.1:7897" {
		t.Errorf("期望读取配置表代理，实际 %q", got)
	}
}
