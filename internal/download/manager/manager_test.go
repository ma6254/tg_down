package manager

import (
	"context"
	"testing"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

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
