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
		Created:   []uint{1, 2},
		Restarted: []uint{4},
		Existing:  []uint{3},
		Skipped:   []manager.Skip{{Link: "l1", Reason: "没有可下载媒体"}},
		Failed:    []manager.Failure{{Link: "l2", Error: "boom"}},
	})
	if len(resp.Created) != 2 || len(resp.Existing) != 1 {
		t.Errorf("created/existing = %+v", resp)
	}
	if len(resp.Restarted) != 1 || resp.Restarted[0] != 4 {
		t.Errorf("restarted = %+v", resp.Restarted)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].Reason != "没有可下载媒体" {
		t.Errorf("skipped = %+v", resp.Skipped)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Error != "boom" {
		t.Errorf("failed = %+v", resp.Failed)
	}
}

// TestNewDownloadStats 确认总览字段映射。
func TestNewDownloadStats(t *testing.T) {
	got := NewDownloadStats(manager.Stats{TotalSpeed: 4096, Running: 3})
	if got.TotalSpeed != 4096 || got.RunningTasks != 3 {
		t.Errorf("DownloadStats = %+v", got)
	}
}

// TestNewDownloadSpeedHistory 确认历史速度的字段映射与空列表不是 null。
func TestNewDownloadSpeedHistory(t *testing.T) {
	from := time.Unix(100, 0)
	to := time.Unix(200, 0)
	got := NewDownloadSpeedHistory(manager.SpeedHistory{
		From:           from,
		To:             to,
		Bucket:         10 * time.Second,
		SampleInterval: time.Second,
		StatsSince:     from,
		AverageSpeed:   300,
		PeakSpeed:      500,
		Points: []manager.SpeedHistoryPoint{
			{At: from, Speed: 250, Running: 2},
		},
	})
	if got.From != 100 || got.To != 200 || got.Bucket != 10 || got.SampleInterval != 1 {
		t.Errorf("时间/粒度 = %+v", got)
	}
	if got.StatsSince != 100 {
		t.Errorf("stats_since = %d，期望 100", got.StatsSince)
	}
	if got.AverageSpeed != 300 || got.PeakSpeed != 500 {
		t.Errorf("汇总 = %d/%d", got.AverageSpeed, got.PeakSpeed)
	}
	if len(got.Points) != 1 || got.Points[0].TotalSpeed != 250 || got.Points[0].RunningTasks != 2 {
		t.Errorf("points = %+v", got.Points)
	}
	if empty := NewDownloadSpeedHistory(manager.SpeedHistory{}); empty.Points == nil {
		t.Error("空结果的 points 应为空数组而不是 null")
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
