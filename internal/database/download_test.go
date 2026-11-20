package database

import (
	"errors"
	"testing"
)

// newDownloadTask 构造一条测试任务。
func newDownloadTask(link string) *DownloadTask {
	return &DownloadTask{
		Link:      link,
		AccountID: 1,
		ChatID:    -100123,
		ChatTitle: "测试频道",
		MessageID: 42,
		FileName:  "clip.mp4",
		FileSize:  1024,
		Kind:      "video",
		MIME:      "video/mp4",
		Status:    DownloadStatusPending,
	}
}

// TestDownloadTaskCreateUnique 确认同一条链接只会创建一次任务。
func TestDownloadTaskCreateUnique(t *testing.T) {
	db := openTestDB(t)

	first := newDownloadTask("https://t.me/a/1")
	created, err := db.CreateDownloadTask(first)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if !created || first.ID == 0 {
		t.Fatalf("首次创建应成功并回填 ID: created=%v id=%d", created, first.ID)
	}

	second := newDownloadTask("https://t.me/a/1")
	created, err = db.CreateDownloadTask(second)
	if err != nil {
		t.Fatalf("重复创建报错: %v", err)
	}
	if created {
		t.Fatal("重复链接不应创建新任务")
	}
	if second.ID != first.ID {
		t.Errorf("重复创建应回填已有 ID %d，实际 %d", first.ID, second.ID)
	}
}

// TestDownloadTaskList 覆盖状态筛选与按 ID 倒序。
func TestDownloadTaskList(t *testing.T) {
	db := openTestDB(t)

	for _, link := range []string{"https://t.me/a/1", "https://t.me/a/2"} {
		if _, err := db.CreateDownloadTask(newDownloadTask(link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpdateDownloadTask(1, map[string]any{"status": DownloadStatusRunning}); err != nil {
		t.Fatal(err)
	}

	all, err := db.ListDownloadTasks("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != 2 {
		t.Fatalf("全部任务应按 ID 倒序: %+v", all)
	}

	running, err := db.ListDownloadTasks(DownloadStatusRunning, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].ID != 1 {
		t.Fatalf("状态筛选结果异常: %+v", running)
	}
}

// TestDownloadTaskUpdateAndPause 覆盖进度更新与「只暂停可暂停状态」的约束。
func TestDownloadTaskUpdateAndPause(t *testing.T) {
	db := openTestDB(t)

	task := newDownloadTask("https://t.me/a/1")
	if _, err := db.CreateDownloadTask(task); err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateDownloadTask(task.ID, map[string]any{"offset": 512, "speed": 128}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDownloadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Offset != 512 || got.Speed != 128 {
		t.Errorf("进度未更新: %+v", got)
	}

	// done 状态不能被暂停覆盖。
	if err := db.UpdateDownloadTask(task.ID, map[string]any{"status": DownloadStatusDone}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetDownloadPaused(task.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetDownloadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DownloadStatusDone {
		t.Errorf("done 被改成了 %q", got.Status)
	}
}

// TestDownloadTaskDelete 覆盖删除与不存在的情况。
func TestDownloadTaskDelete(t *testing.T) {
	db := openTestDB(t)

	task := newDownloadTask("https://t.me/a/1")
	if _, err := db.CreateDownloadTask(task); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteDownloadTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDownloadTask(task.ID); !errors.Is(err, ErrDownloadNotFound) {
		t.Fatalf("期望 ErrDownloadNotFound，实际 %v", err)
	}
	if err := db.DeleteDownloadTask(task.ID); !errors.Is(err, ErrDownloadNotFound) {
		t.Fatalf("重复删除期望 ErrDownloadNotFound，实际 %v", err)
	}
}

// TestResetRunningDownloads 确认重启后 running 任务被标记为 paused。
func TestResetRunningDownloads(t *testing.T) {
	db := openTestDB(t)

	task := newDownloadTask("https://t.me/a/1")
	if _, err := db.CreateDownloadTask(task); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDownloadTask(task.ID, map[string]any{"status": DownloadStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResetRunningDownloads(); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetDownloadTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != DownloadStatusPaused || got.Error == "" {
		t.Errorf("重启后任务 = %+v，期望 paused 且带原因", got)
	}
}

// TestDropLegacyThumbnailColumn 确认旧 BLOB 列会被删除且不影响其它数据。
func TestDropLegacyThumbnailColumn(t *testing.T) {
	db := openTestDB(t)

	if err := db.Exec("ALTER TABLE download_tasks ADD COLUMN thumbnail BLOB").Error; err != nil {
		t.Fatalf("添加旧列失败: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO download_tasks (link, status, thumbnail) VALUES (?, ?, ?)",
		"legacy", DownloadStatusDone, []byte{1, 2, 3},
	).Error; err != nil {
		t.Fatalf("写入旧数据失败: %v", err)
	}
	if !db.hasColumn("download_tasks", "thumbnail") {
		t.Fatal("旧列没有加上")
	}

	if err := db.dropLegacyThumbnailColumn(); err != nil {
		t.Fatalf("删除旧列失败: %v", err)
	}
	if db.hasColumn("download_tasks", "thumbnail") {
		t.Fatal("旧列仍然存在")
	}

	// 行数据保留，只是列没了。
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM download_tasks").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("任务行数 = %d，期望 1", count)
	}

	// 幂等：列不存在时再调用不应报错。
	if err := db.dropLegacyThumbnailColumn(); err != nil {
		t.Errorf("重复迁移报错: %v", err)
	}
}

// TestInstallDropsLegacyThumbnail 确认启动迁移会自动清掉旧列。
func TestInstallDropsLegacyThumbnail(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec("ALTER TABLE download_tasks ADD COLUMN thumbnail BLOB").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if db.hasColumn("download_tasks", "thumbnail") {
		t.Error("Install 后旧列仍然存在")
	}
}
