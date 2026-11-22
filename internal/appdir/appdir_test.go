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

// TestRelSessionPath 确认数据目录内的文件存成相对路径，目录外保留绝对路径。
func TestRelSessionPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	inside := filepath.Join(home, "sessions", "mtproto-8613800000000.json")
	if got := RelSessionPath(inside); got != "sessions/mtproto-8613800000000.json" {
		t.Errorf("数据目录内 = %q", got)
	}
	// 已经相对的路径只做分隔符归一，不重复加前缀。
	if got := RelSessionPath("./sessions/mtproto-1.json"); got != "sessions/mtproto-1.json" {
		t.Errorf("相对路径 = %q", got)
	}
	if got := RelSessionPath(""); got != "" {
		t.Errorf("空路径 = %q", got)
	}
	outside := filepath.Join(t.TempDir(), "other.json")
	if got := RelSessionPath(outside); !filepath.IsAbs(got) {
		t.Errorf("数据目录外应保留绝对路径，实际 %q", got)
	}
}

// TestAbsSessionPath 确认读库时把相对路径还原到数据目录。
func TestAbsSessionPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	want := filepath.Join(home, "sessions", "mtproto-1.json")
	if got := AbsSessionPath("sessions/mtproto-1.json"); got != want {
		t.Errorf("相对路径还原 = %q，期望 %q", got, want)
	}
	abs := filepath.Join(t.TempDir(), "custom.json")
	if got := AbsSessionPath(abs); got != abs {
		t.Errorf("绝对路径应原样返回，实际 %q", got)
	}
	if got := AbsSessionPath(""); got != "" {
		t.Errorf("空路径 = %q", got)
	}
}

// TestAbsSessionPathLegacyCWD 确认旧布局（相对工作目录）的文件仍能找到。
func TestAbsSessionPathLegacyCWD(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	work := t.TempDir()
	t.Chdir(work)

	legacy := filepath.Join("sessions", "mtproto-legacy.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := AbsSessionPath(filepath.ToSlash(legacy)); got != legacy {
		t.Errorf("旧路径 = %q，期望 %q", got, legacy)
	}
}

// TestExeDir 确认默认可执行文件目录，临时构建产物则回退。
func TestExeDir(t *testing.T) {
	sep := string(filepath.Separator)
	tools := filepath.Join(sep, "tg_down_tools")

	if got := exeDir(filepath.Join(tools, "tg_down.exe")); got != tools {
		t.Errorf("普通二进制目录 = %q，期望 %q", got, tools)
	}

	// go run / go test 的临时二进制没有稳定位置。
	if got := exeDir(filepath.Join(os.TempDir(), "tg_down.exe")); got != "" {
		t.Errorf("临时目录 = %q，期望空", got)
	}
	if got := exeDir(filepath.Join(tools, "appdir.test.exe")); got != "" {
		t.Errorf("测试二进制 = %q，期望空", got)
	}
	if got := exeDir(filepath.Join(tools, "go-build123", "b001", "exe", "tg_down.exe")); got != "" {
		t.Errorf("go-build 缓存 = %q，期望空", got)
	}

	cache := filepath.Join(sep, "tg_down_cache")
	t.Setenv("GOCACHE", cache)
	if got := exeDir(filepath.Join(cache, "tg_down.exe")); got != "" {
		t.Errorf("GOCACHE 下 = %q，期望空", got)
	}
}

// TestDataDirFallsBackForTestBinary 确认测试二进制使用 ~/.tg_down。
func TestDataDirFallsBackForTestBinary(t *testing.T) {
	t.Setenv(EnvHome, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("当前环境没有用户主目录")
	}
	got, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != filepath.Join(home, DirName) {
		t.Errorf("DataDir = %q，期望 %q", got, filepath.Join(home, DirName))
	}
}
