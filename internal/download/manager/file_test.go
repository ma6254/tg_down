package manager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestOpenFile 覆盖已完成文件的读取、未完成状态与路径逃逸。
func TestOpenFile(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	root := t.TempDir()
	inside := filepath.Join(root, "photo.jpg")
	if err := os.WriteFile(inside, []byte("image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jpg")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 放在下载根目录旁边，用于验证 "root/../file" 也会被拦下。
	escape := filepath.Join(filepath.Dir(root), "escape-"+filepath.Base(root)+".jpg")
	if err := os.WriteFile(escape, []byte("escape"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}

	tasks := map[string]*database.DownloadTask{
		"done": {
			Link: "https://t.me/a/1", FileName: "photo.jpg", MIME: "image/jpeg",
			Status: database.DownloadStatusDone, SavePath: inside,
		},
		"pending": {
			Link: "https://t.me/a/2", FileName: "pending.jpg", MIME: "image/jpeg",
			Status: database.DownloadStatusPending, SavePath: inside,
		},
		"missing": {
			Link: "https://t.me/a/3", FileName: "missing.jpg",
			Status: database.DownloadStatusDone, SavePath: filepath.Join(root, "missing.jpg"),
		},
		"outside": {
			Link: "https://t.me/a/4", FileName: "outside.jpg",
			Status: database.DownloadStatusDone, SavePath: outside,
		},
		"escape": {
			Link: "https://t.me/a/5", FileName: "escape.jpg",
			Status: database.DownloadStatusDone, SavePath: filepath.Join(root, "..", filepath.Base(escape)),
		},
		"no_path": {
			Link: "https://t.me/a/6", FileName: "empty.jpg",
			Status: database.DownloadStatusDone,
		},
	}
	for _, task := range tasks {
		if _, err := db.CreateDownloadTask(task); err != nil {
			t.Fatalf("创建任务 %s: %v", task.Link, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	m := New(Config{RootDir: root, Workers: 1})

	file, info, err := m.OpenFile(context.Background(), tasks["done"].ID)
	if err != nil {
		t.Fatalf("打开已完成文件: %v", err)
	}
	data, err := io.ReadAll(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Errorf("关闭文件: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("读取文件: %v", err)
	}
	if string(data) != "image-bytes" {
		t.Errorf("文件内容 = %q", data)
	}
	if info.Name != "photo.jpg" || info.MIME != "image/jpeg" || info.Size != int64(len("image-bytes")) {
		t.Errorf("文件信息 = %+v", info)
	}

	cases := map[string]struct {
		task string
		want error
	}{
		"未完成":  {"pending", ErrConflict},
		"文件缺失": {"missing", ErrFileNotFound},
		"目录外":  {"outside", ErrFileNotFound},
		"路径逃逸": {"escape", ErrFileNotFound},
		"空路径":  {"no_path", ErrFileNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			file, _, err := m.OpenFile(context.Background(), tasks[tc.task].ID)
			if file != nil {
				_ = file.Close()
				t.Fatal("失败时不应返回文件句柄")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("错误 = %v，期望 %v", err, tc.want)
			}
		})
	}
}
