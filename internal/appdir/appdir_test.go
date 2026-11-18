package appdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataDirEnvOverride 确认 TG_DOWN_HOME 优先。
func TestDataDirEnvOverride(t *testing.T) {
	custom := t.TempDir()
	t.Setenv(EnvHome, custom)

	got, err := DataDir()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got != custom {
		t.Errorf("got %q，期望 %q", got, custom)
	}
}

// TestDataDirDefault 确认默认落在用户主目录下的 .tg_down。
func TestDataDirDefault(t *testing.T) {
	t.Setenv(EnvHome, "")

	got, err := DataDir()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !strings.HasSuffix(got, DirName) {
		t.Errorf("默认目录 %q 未以 %q 结尾", got, DirName)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("当前环境没有用户主目录")
	}
	if got != filepath.Join(home, DirName) {
		t.Errorf("got %q，期望 %q", got, filepath.Join(home, DirName))
	}
}

// TestDBPathCreatesDir 确认 db 路径的目录会被创建。
func TestDBPathCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "home")
	t.Setenv(EnvHome, dir)

	path, err := DBPath()
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if filepath.Base(path) != "data.db" {
		t.Errorf("文件名 = %q", filepath.Base(path))
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("数据目录未创建: %v", err)
	}
}
