package manager

import (
	"context"
	"testing"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestEnqueueMediaAllowOverLimit 确认超过上限的媒体默认跳过，显式强制时仍落库。
func TestEnqueueMediaAllowOverLimit(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	media := &mtproto.Media{
		Link:         "https://t.me/a/4",
		FileName:     "big.mp4",
		Size:         mtproto.MaxFileSize + 1,
		Downloadable: false,
	}

	var skipped CreateResult
	m.enqueueMedia(db, mtproto.Session{}, 1, mtproto.Peer{}, media, "", 0, false, false, &skipped)
	if len(skipped.Created) != 0 || len(skipped.Skipped) != 1 {
		t.Fatalf("未强制时结果 = %+v，期望 skipped 1 条", skipped)
	}

	var forced CreateResult
	m.enqueueMedia(db, mtproto.Session{}, 1, mtproto.Peer{}, media, "", 0, false, true, &forced)
	if len(forced.Created) != 1 {
		t.Fatalf("强制时结果 = %+v，期望创建 1 个任务", forced)
	}
	task, err := db.GetDownloadTask(forced.Created[0])
	if err != nil {
		t.Fatal(err)
	}
	if !task.AllowOverLimit {
		t.Error("任务没有落库 allow_over_limit 标记")
	}
	if task.FileSize != media.Size {
		t.Errorf("file_size = %d，期望 %d", task.FileSize, media.Size)
	}
}
