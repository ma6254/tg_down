package manager

import (
	"context"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestCanRestartTask 确认覆盖模式只重置已经停下的任务。
func TestCanRestartTask(t *testing.T) {
	tests := map[string]bool{
		database.DownloadStatusPending: false,
		database.DownloadStatusRunning: false,
		database.DownloadStatusPaused:  true,
		database.DownloadStatusDone:    true,
		database.DownloadStatusFailed:  true,
		"unknown":                      false,
	}
	for status, want := range tests {
		if got := canRestartTask(status); got != want {
			t.Errorf("canRestartTask(%q) = %v，期望 %v", status, got, want)
		}
	}
}

// TestPickTaskMedia 确认相册任务按 MessageID 选中对应媒体。
func TestPickTaskMedia(t *testing.T) {
	first := &mtproto.Media{MessageID: 10, FileName: "a.jpg"}
	second := &mtproto.Media{MessageID: 11, FileName: "b.jpg"}
	result := mtproto.LinkResult{Media: first, Medias: []*mtproto.Media{first, second}}

	if got := pickTaskMedia(result, 11); got != second {
		t.Errorf("按 ID 匹配 = %+v，期望 b.jpg", got)
	}
	if got := pickTaskMedia(result, 99); got != first {
		t.Errorf("未匹配时回退主媒体 = %+v", got)
	}
	if got := pickTaskMedia(mtproto.LinkResult{}, 1); got != nil {
		t.Errorf("没有媒体时应返回 nil，实际 %+v", got)
	}
}

// TestSelectMedias 确认 single 模式只取目标消息，默认仍展开相册。
func TestSelectMedias(t *testing.T) {
	first := &mtproto.Media{MessageID: 10, FileName: "a.jpg"}
	second := &mtproto.Media{MessageID: 11, FileName: "b.jpg"}
	album := &mtproto.LinkResult{
		MessageID: 10,
		Media:     first,
		Medias:    []*mtproto.Media{first, second},
	}

	single := selectMedias(album, true)
	if len(single) != 1 || single[0] != first {
		t.Errorf("single = %+v，期望只含 a.jpg", single)
	}
	all := selectMedias(album, false)
	if len(all) != 2 {
		t.Errorf("默认应展开相册，实际 %+v", all)
	}

	// 非相册消息：两种模式都只有主媒体。
	solo := &mtproto.LinkResult{MessageID: 7, Media: &mtproto.Media{MessageID: 7}}
	if got := selectMedias(solo, true); len(got) != 1 {
		t.Errorf("单条消息 single = %+v", got)
	}
	if got := selectMedias(solo, false); len(got) != 1 {
		t.Errorf("单条消息默认 = %+v", got)
	}

	// 没有媒体时返回 nil。
	if got := selectMedias(&mtproto.LinkResult{}, true); got != nil {
		t.Errorf("没有媒体时应返回 nil，实际 %+v", got)
	}
}

// TestCollectCommentMedias 确认评论媒体按顺序取、跳过纯文字评论并截断。
func TestCollectCommentMedias(t *testing.T) {
	comments := []mtproto.Comment{
		{Media: &mtproto.Media{MessageID: 1}},
		{Text: "纯文字评论"},
		{Media: &mtproto.Media{MessageID: 2}},
		{Media: &mtproto.Media{MessageID: 3}},
	}

	medias, truncated := collectCommentMedias(comments, 2)
	if len(medias) != 2 || medias[0].MessageID != 1 || medias[1].MessageID != 2 {
		t.Fatalf("medias = %+v", medias)
	}
	if !truncated {
		t.Error("超过上限时应标记 truncated")
	}

	medias, truncated = collectCommentMedias(comments, 10)
	if len(medias) != 3 || truncated {
		t.Errorf("未超过上限: medias=%+v truncated=%v", medias, truncated)
	}

	medias, truncated = collectCommentMedias([]mtproto.Comment{{Text: "只有文字"}}, 10)
	if len(medias) != 0 || truncated {
		t.Errorf("没有媒体: medias=%+v truncated=%v", medias, truncated)
	}
}

// TestSnapshotTerminalStatusWins 确认终态不会被内存进度覆盖成 running。
//
// 完成时先落库 done、后注销 run；如果 snapshot 无条件用 run 覆盖，
// 发出的 task.update 会是「running + 100%」，前端只能等轮询才变成已完成。
func TestSnapshotTerminalStatusWins(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	run := &downloadRun{done: make(chan struct{})}
	run.setOffset(100)
	m.runs[7] = run

	done := m.snapshot(database.DownloadTask{
		ID:       7,
		Status:   database.DownloadStatusDone,
		Offset:   100,
		FileSize: 100,
	})
	if done.Row.Status != database.DownloadStatusDone {
		t.Errorf("done 快照状态 = %q，期望 %q", done.Row.Status, database.DownloadStatusDone)
	}
	if done.Offset != 100 {
		t.Errorf("done 快照 offset = %d，期望 100", done.Offset)
	}

	running := m.snapshot(database.DownloadTask{
		ID:       7,
		Status:   database.DownloadStatusRunning,
		FileSize: 100,
	})
	if running.Row.Status != database.DownloadStatusRunning || running.Offset != 100 {
		t.Errorf("running 快照 = %+v，期望 running/100", running)
	}
}

// TestStats 确认总速度只统计运行中的任务，并跳过暂停/已删除的 run。
func TestStats(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	first := &downloadRun{speed: 1000}
	second := &downloadRun{speed: 2500}
	paused := &downloadRun{paused: true, speed: 800}
	deleted := &downloadRun{deleted: true, speed: 400}

	m.mu.Lock()
	m.runs[1] = first
	m.runs[2] = second
	m.runs[3] = paused
	m.runs[4] = deleted
	m.mu.Unlock()

	stats := m.Stats()
	if stats.TotalSpeed != 3500 || stats.Running != 2 {
		t.Errorf("Stats = %+v，期望 TotalSpeed=3500 Running=2", stats)
	}

	if empty := (&Manager{}).Stats(); empty.TotalSpeed != 0 || empty.Running != 0 {
		t.Errorf("空管理器的 Stats = %+v，期望零值", empty)
	}
}

// TestMediaURL 确认封面地址直接采用注入函数返回的完整 URL，不再重复拼前缀。
func TestMediaURL(t *testing.T) {
	m := &Manager{thumbnailURL: func(mtproto.Session, mtproto.Thumb) string {
		return "/api/v1/media/thumbnail/abc"
	}}
	if got := m.mediaURL(mtproto.Session{}, mtproto.Thumb{}); got != "/api/v1/media/thumbnail/abc" {
		t.Errorf("mediaURL = %q", got)
	}

	empty := &Manager{thumbnailURL: func(mtproto.Session, mtproto.Thumb) string { return "" }}
	if got := empty.mediaURL(mtproto.Session{}, mtproto.Thumb{}); got != "" {
		t.Errorf("空 URL = %q", got)
	}
	if got := (&Manager{}).mediaURL(mtproto.Session{}, mtproto.Thumb{}); got != "" {
		t.Errorf("未注入时应为空，实际 %q", got)
	}
}
