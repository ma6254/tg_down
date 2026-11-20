package download

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeName 覆盖非法字符、首尾空白、保留名与截断。
func TestSafeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"非法字符替换", `a<>:"/\|?*b`, 0, "a_________b"},
		{"控制字符替换", "a\x00b\x1fc", 0, "a_b_c"},
		{"去掉首尾空格与点", "  ..hello..  ", 0, "hello"},
		{"Windows 保留名", "CON", 0, "_CON"},
		{"保留名带扩展名", "com1.txt", 0, "_com1.txt"},
		{"空字符串", "   ", 0, ""},
		{"按字符截断", strings.Repeat("剧", 10), 5, "剧剧剧剧剧"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeName(tt.in, tt.max); got != tt.want {
				t.Errorf("SafeName(%q, %d) = %q，期望 %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

// TestNewPlanSkipExisting 同名同大小视为已完成。
func TestNewPlanSkipExisting(t *testing.T) {
	root := t.TempDir()
	chat := "某群"
	dir := filepath.Join(root, chat)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(path, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := NewPlan(root, chat, "video.mp4", 10, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Skip || plan.FinalPath != path {
		t.Fatalf("plan = %+v，期望跳过 %s", plan, path)
	}

	// force 时即使大小一致也要重新下载。
	forced, err := NewPlan(root, chat, "video.mp4", 10, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if forced.Skip {
		t.Error("force 时不应跳过")
	}
}

// TestNewPlanConflict 同名但大小不同时追加序号。
func TestNewPlanConflict(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "某群")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video.mp4"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := NewPlan(root, "某群", "video.mp4", 20, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Skip {
		t.Fatal("大小不同不应跳过")
	}
	if !strings.Contains(plan.FinalPath, "video (1).mp4") {
		t.Errorf("FinalPath = %q，期望带 (1) 后缀", plan.FinalPath)
	}
}

// TestNewPlanResume 已有 .part 时按 PartSize 对齐续传。
func TestNewPlanResume(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "某群")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(dir, "video.mp4.part")
	// 1.2 MiB → 向下对齐到 1 MiB。
	if err := os.WriteFile(part, make([]byte, 1258291), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := NewPlan(root, "某群", "video.mp4", 10*1024*1024, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Offset != 1024*1024 {
		t.Errorf("Offset = %d，期望 %d", plan.Offset, 1024*1024)
	}

	// force 时从零开始。
	forced, err := NewPlan(root, "某群", "video.mp4", 10*1024*1024, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if forced.Offset != 0 {
		t.Errorf("force 时 Offset = %d，期望 0", forced.Offset)
	}
}

// TestNewPlanFlat 不按会话建子目录。
func TestNewPlanFlat(t *testing.T) {
	root := t.TempDir()
	plan, err := NewPlan(root, "某群", "video.mp4", 100, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(plan.FinalPath) != root {
		t.Errorf("FinalPath = %q，期望直接在根目录下", plan.FinalPath)
	}
}
