package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFileExists 确认只把普通文件视为存在。
func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !FileExists(file) {
		t.Error("存在的文件应返回 true")
	}
	if FileExists(dir) {
		t.Error("目录不算存在")
	}
	if FileExists(filepath.Join(dir, "missing.txt")) {
		t.Error("不存在的文件应返回 false")
	}
	if FileExists("") {
		t.Error("空路径应返回 false")
	}
	if !FileExists("  " + file + "\t") {
		t.Error("路径两端空白应被忽略")
	}
}

// TestReadFileBytes 确认不存在的文件与空文件都按未命中处理。
func TestReadFileBytes(t *testing.T) {
	dir := t.TempDir()

	if _, ok := ReadFileBytes(filepath.Join(dir, "missing")); ok {
		t.Error("不存在的文件应未命中")
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadFileBytes(empty); ok {
		t.Error("空文件应未命中")
	}

	file := filepath.Join(dir, "data")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, ok := ReadFileBytes(file)
	if !ok || string(data) != "hello" {
		t.Errorf("读取 = %q，%v", data, ok)
	}
}

// TestWriteFileAtomic 确认自动建目录、写入完整内容且不留临时文件。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "b.bin")

	if err := WriteFileAtomic(path, []byte("payload")); err != nil {
		t.Fatalf("写入: %v", err)
	}
	data, ok := ReadFileBytes(path)
	if !ok || string(data) != "payload" {
		t.Fatalf("读取 = %q，%v", data, ok)
	}

	// 临时文件必须清干净，否则缓存目录会越积越多。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Errorf("目录里应只有目标文件，实际 %+v", entries)
	}

	// 覆盖写：内容整体替换。
	if err := WriteFileAtomic(path, []byte("second")); err != nil {
		t.Fatalf("覆盖写: %v", err)
	}
	if data, _ := ReadFileBytes(path); string(data) != "second" {
		t.Errorf("覆盖后 = %q", data)
	}
}

// TestWriteFileAtomicEmpty 空内容不落盘。
func TestWriteFileAtomicEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.bin")
	if err := WriteFileAtomic(path, nil); err == nil {
		t.Error("空内容应返回错误")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("空内容不应创建文件")
	}
}
