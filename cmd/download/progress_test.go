package download

import (
	"io"
	"testing"

	"github.com/schollz/progressbar/v3"

	downloadsvc "github.com/ma6254/tg_down/internal/download"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestPerFileProgressResets 确认单文件模式下进度条按当前文件重置总量。
func TestPerFileProgressResets(t *testing.T) {
	bar := progressbar.NewOptions64(1,
		progressbar.OptionSetWriter(io.Discard),
		progressbar.OptionSetRenderBlankState(false),
	)
	p := &downloadProgress{
		bar:     bar,
		total:   150,
		files:   2,
		perFile: true,
		last:    make(map[string]int64),
		started: make(map[string]bool),
	}

	first := mtproto.MediaJob{ChatTitle: "频道", FileName: "a.mp4", Size: 100}
	p.start(first, downloadsvc.Plan{Offset: 0})
	if state := bar.State(); state.Max != 100 || state.CurrentNum != 0 {
		t.Fatalf("首个文件起始状态 = max %d current %d，期望 100/0", state.Max, state.CurrentNum)
	}

	p.tick(first, 40, 100)
	if state := bar.State(); state.CurrentNum != 40 {
		t.Fatalf("首个文件进度 = %d，期望 40", state.CurrentNum)
	}

	p.done(mtproto.DownloadResult{ChatTitle: "频道", FileName: "a.mp4", Size: 100})
	if state := bar.State(); state.CurrentNum != 100 {
		t.Fatalf("首个文件完成后 = %d，期望 100", state.CurrentNum)
	}

	// 第二个文件从 10 字节续传：总量应切换成 50，而不是累计 150。
	second := mtproto.MediaJob{ChatTitle: "频道", FileName: "b.mp4", Size: 50}
	p.start(second, downloadsvc.Plan{Offset: 10})
	if state := bar.State(); state.Max != 50 || state.CurrentNum != 10 {
		t.Fatalf("第二个文件起始状态 = max %d current %d，期望 50/10", state.Max, state.CurrentNum)
	}

	p.tick(second, 30, 50)
	if state := bar.State(); state.CurrentNum != 30 {
		t.Fatalf("第二个文件进度 = %d，期望 30", state.CurrentNum)
	}
}
