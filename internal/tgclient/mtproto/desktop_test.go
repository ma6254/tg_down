package mtproto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocateTDataExplicit 覆盖显式传入 tdata 路径的分支。
func TestLocateTDataExplicit(t *testing.T) {
	dir := t.TempDir()
	got, err := locateTData(dir)
	if err != nil {
		t.Fatalf("显式目录应可解析: %v", err)
	}
	if got != dir {
		t.Errorf("got %q，期望 %q", got, dir)
	}
}

// TestLocateTDataMissing 确认不存在的路径会给出明确错误。
func TestLocateTDataMissing(t *testing.T) {
	_, err := locateTData(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("期望报错，实际通过")
	}
	if !strings.Contains(err.Error(), "不可用") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestLocateTDataNotDir 确认传文件时报错。
func TestLocateTDataNotDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key_datas")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	if _, err := locateTData(file); err == nil {
		t.Fatal("文件路径应报错")
	}
}

// TestTDataCandidates 确认常见安装位置都能被枚举出来。
func TestTDataCandidates(t *testing.T) {
	dirs := tdataCandidates()
	if len(dirs) == 0 {
		t.Skip("当前平台没有已知的 tdata 位置（可能是非 Windows）")
	}
	for _, dir := range dirs {
		if !strings.Contains(dir, "Telegram") {
			t.Errorf("候选路径异常: %s", dir)
		}
	}
}
