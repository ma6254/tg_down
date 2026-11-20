package server

import (
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/download/manager"
)

// TestNewDownloadTask 确认任务快照的字段映射与实时进度覆盖。
func TestNewDownloadTask(t *testing.T) {
	created := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	task := manager.Task{
		Row: database.DownloadTask{
			ID:           7,
			Link:         "https://t.me/a/1",
			ChatTitle:    "频道",
			FileName:     "a.jpg",
			FileSize:     1024,
			Status:       database.DownloadStatusRunning,
			Offset:       512,
			Speed:        100,
			ThumbnailURL: "/api/v1/media/thumbnail/t1",
			CreatedAt:    created,
			UpdatedAt:    created,
		},
		Offset: 900,
		Speed:  200,
	}

	got := NewDownloadTask(task)
	if got.ID != 7 || got.FileName != "a.jpg" || got.Status != DownloadStatusRunning {
		t.Errorf("任务字段 = %+v", got)
	}
	// 接口用内存里的实时进度，而不是数据库里落盘的那个。
	if got.Offset != 900 || got.Speed != 200 {
		t.Errorf("进度 = %d/%d，期望 900/200", got.Offset, got.Speed)
	}
	if got.CreatedAt != created.Unix() {
		t.Errorf("created_at = %d", got.CreatedAt)
	}
}

// TestNewDownloadTasksEmpty 确认空列表返回空切片而不是 nil。
func TestNewDownloadTasksEmpty(t *testing.T) {
	got := NewDownloadTasks(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("空列表 = %#v", got)
	}
}

// TestNewDownloadCreateResponse 确认跳过与失败原因被原样带出。
func TestNewDownloadCreateResponse(t *testing.T) {
	resp := NewDownloadCreateResponse(manager.CreateResult{
		Created:  []uint{1, 2},
		Existing: []uint{3},
		Skipped:  []manager.Skip{{Link: "l1", Reason: "没有可下载媒体"}},
		Failed:   []manager.Failure{{Link: "l2", Error: "boom"}},
	})
	if len(resp.Created) != 2 || len(resp.Existing) != 1 {
		t.Errorf("created/existing = %+v", resp)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].Reason != "没有可下载媒体" {
		t.Errorf("skipped = %+v", resp.Skipped)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Error != "boom" {
		t.Errorf("failed = %+v", resp.Failed)
	}
}

// TestNewDownloadEvent 确认三种事件的数据形态。
func TestNewDownloadEvent(t *testing.T) {
	update := NewDownloadEvent(manager.Event{
		Type: manager.EventTaskUpdate,
		Task: manager.Task{Row: database.DownloadTask{ID: 5, Status: database.DownloadStatusDone}},
	})
	if update.Type != manager.EventTaskUpdate {
		t.Errorf("事件类型 = %q", update.Type)
	}
	if task, ok := update.Data.(DownloadTask); !ok || task.ID != 5 {
		t.Errorf("task.update 数据 = %#v", update.Data)
	}

	progress := NewDownloadEvent(manager.Event{
		Type: manager.EventTaskProgress, ID: 5, Offset: 10, Speed: 2, Size: 100,
	})
	data, ok := progress.Data.(map[string]any)
	if !ok || data["offset"] != int64(10) || data["size"] != int64(100) {
		t.Errorf("task.progress 数据 = %#v", progress.Data)
	}

	deleted := NewDownloadEvent(manager.Event{Type: manager.EventTaskDeleted, ID: 5})
	if data, ok := deleted.Data.(map[string]any); !ok || data["id"] != uint(5) {
		t.Errorf("task.deleted 数据 = %#v", deleted.Data)
	}
}
