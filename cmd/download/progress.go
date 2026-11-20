package download

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"

	downloadsvc "github.com/ma6254/tg_down/internal/download"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// spinnerFrames 是进度条描述前缀的旋转字符（npm 风格 braille spinner）。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// downloadTheme 与 FictionCraft 的进度条保持一致：青色填充、深灰底、竖线端点。
var downloadTheme = progressbar.Theme{
	Saucer:        "[cyan]█",
	SaucerPadding: "[dark_gray]░",
	BarStart:      "│",
	BarEnd:        "│",
}

// downloadProgress 用一条进度条展示下载：
// 默认单文件串行时，进度条表示「当前文件」的进度，换文件时清零重来；
// 并发下载多个文件时退化为所有文件的总字节进度。
// 描述区显示文件名、字节数、实时速度与剩余时间，文件开始/结束以日志形式显示在进度条上方。
type downloadProgress struct {
	bar     *progressbar.ProgressBar
	total   int64
	files   int
	perFile bool

	mu          sync.Mutex
	bytesDone   int64
	currentName string
	currentSize int64
	sampled     int64
	sampledAt   time.Time
	speed       float64
	active      int
	last        map[string]int64
	started     map[string]bool

	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
}

func newDownloadProgress(total int64, files, concurrency int) *downloadProgress {
	if total <= 0 {
		total = 1
	}
	perFile := concurrency <= 1
	max := total
	if perFile {
		// 单文件模式下总量先占位，start 时改成当前文件大小。
		max = 1
	}

	bar := progressbar.NewOptions64(max,
		progressbar.OptionSetDescription("准备下载"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetPredictTime(false),
		progressbar.OptionSetTheme(downloadTheme),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionSetRenderBlankState(true),
	)

	p := &downloadProgress{
		bar:     bar,
		total:   total,
		files:   files,
		perFile: perFile,
		last:    make(map[string]int64),
		started: make(map[string]bool),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go p.spin()
	return p
}

// spin 定时刷新描述区：旋转图标、文件名、速度与 ETA。
func (p *downloadProgress) spin() {
	defer close(p.stopped)

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for i := 0; ; i++ {
		select {
		case <-ticker.C:
			frame := spinnerFrames[i%len(spinnerFrames)]
			p.bar.Describe(p.describe(frame))
		case <-p.stop:
			return
		}
	}
}

// describe 按当前模式拼一行描述；单文件模式只显示当前文件。
func (p *downloadProgress) describe(frame string) string {
	p.mu.Lock()
	active := p.active
	speed := p.speed
	done := p.bytesDone
	name := p.currentName
	size := p.currentSize
	p.mu.Unlock()

	if p.perFile {
		desc := fmt.Sprintf("[cyan]%s[reset] 准备下载", frame)
		if active > 0 && name != "" {
			desc = fmt.Sprintf("[cyan]%s[reset] %s", frame, truncateName(name, 36))
			if size > 0 {
				desc += fmt.Sprintf(" · %s / %s",
					utils.FormatBytes(done), utils.FormatBytes(size))
			}
			if speed > 0 {
				desc += " · " + utils.FormatBytes(int64(speed)) + "/s"
				if remaining := size - done; remaining > 0 {
					eta := time.Duration(float64(remaining)/speed) * time.Second
					desc += " · 剩余 " + formatETA(eta)
				}
			}
		}
		return desc
	}

	desc := fmt.Sprintf("[cyan]%s[reset] 准备下载", frame)
	if active > 0 {
		desc = fmt.Sprintf("[cyan]%s[reset] 正在下载 %d 个文件", frame, active)
	}
	// 自己拼字节数与速度：只保留一套数字，避免和库内置计数重复。
	desc += fmt.Sprintf(" · %s / %s",
		utils.FormatBytes(done), utils.FormatBytes(p.total))
	if speed > 0 {
		desc += " · " + utils.FormatBytes(int64(speed)) + "/s"
		if remaining := p.total - done; remaining > 0 {
			eta := time.Duration(float64(remaining)/speed) * time.Second
			desc += " · 剩余 " + formatETA(eta)
		}
	}
	return desc
}

// truncateName 按字符截断过长的文件名，避免描述区挤爆一行。
func truncateName(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	return string(runes[:limit-1]) + "…"
}

// start 在文件真正开始传输时登记进度；单文件模式把进度条切换到当前文件。
func (p *downloadProgress) start(job mtproto.MediaJob, plan downloadsvc.Plan) {
	key := progressKey(job)

	p.mu.Lock()
	p.active++
	p.last[key] = plan.Offset
	p.started[key] = true
	if p.perFile {
		p.currentName = job.FileName
		p.currentSize = job.Size
		p.bytesDone = plan.Offset
		p.sampled = plan.Offset
		p.sampledAt = time.Now()
		p.speed = 0
	} else if plan.Offset > 0 {
		p.bytesDone += plan.Offset
		p.sampled = p.bytesDone
		if p.sampledAt.IsZero() {
			p.sampledAt = time.Now()
		}
	}
	p.mu.Unlock()

	if p.perFile {
		// 换文件时进度条归零，总量改成当前文件大小。
		p.bar.Reset()
		max := job.Size
		if max <= 0 {
			max = 1
		}
		p.bar.ChangeMax64(max)
		_ = p.bar.Set64(plan.Offset)
	} else if plan.Offset > 0 {
		_ = p.bar.Add64(plan.Offset)
	}

	if plan.Offset > 0 {
		p.logf("[续传] %s（从 %s 继续）", job.FileName, utils.FormatBytes(plan.Offset))
		return
	}
	p.logf("[开始] %s（%s）", job.FileName, utils.FormatBytes(job.Size))
}

// tick 累计本次新增的字节并刷新进度条。
func (p *downloadProgress) tick(job mtproto.MediaJob, done, _ int64) {
	key := progressKey(job)

	p.mu.Lock()
	last := p.last[key]
	delta := done - last
	if delta > 0 {
		p.last[key] = done
	}

	if p.perFile {
		if done > p.bytesDone {
			p.bytesDone = done
		}
		now := time.Now()
		if p.sampledAt.IsZero() {
			p.sampledAt = now
			p.sampled = done
		} else if elapsed := now.Sub(p.sampledAt); elapsed >= 500*time.Millisecond {
			p.speed = float64(done-p.sampled) / elapsed.Seconds()
			p.sampled = done
			p.sampledAt = now
		}
		p.mu.Unlock()
		if delta > 0 {
			_ = p.bar.Set64(done)
		}
		return
	}
	p.mu.Unlock()

	if delta > 0 {
		p.account(delta)
	}
}

// account 计入新字节并更新速度采样。
func (p *downloadProgress) account(delta int64) {
	if delta <= 0 {
		return
	}

	p.mu.Lock()
	p.bytesDone += delta
	now := time.Now()
	if p.sampledAt.IsZero() {
		p.sampledAt = now
		p.sampled = p.bytesDone
	} else if elapsed := now.Sub(p.sampledAt); elapsed >= 500*time.Millisecond {
		p.speed = float64(p.bytesDone-p.sampled) / elapsed.Seconds()
		p.sampled = p.bytesDone
		p.sampledAt = now
	}
	p.mu.Unlock()

	_ = p.bar.Add64(delta)
}

// advance 只推进汇总进度，不计入速度（跳过/失败/续传补齐用）。
func (p *downloadProgress) advance(delta int64) {
	if delta <= 0 {
		return
	}
	p.mu.Lock()
	p.bytesDone += delta
	p.mu.Unlock()
	_ = p.bar.Add64(delta)
}

// done 处理单个文件的结束：单文件模式补满当前进度条，聚合模式补足总进度避免卡住。
func (p *downloadProgress) done(result mtproto.DownloadResult) {
	key := result.ChatTitle + "\x00" + result.FileName

	p.mu.Lock()
	started := p.started[key]
	last := p.last[key]
	delete(p.last, key)
	delete(p.started, key)
	if started && p.active > 0 {
		p.active--
	}
	p.mu.Unlock()

	if p.perFile {
		switch {
		case result.Err != nil:
			p.logf("[失败] %s：%v", result.FileName, result.Err)
		case result.Skipped:
			p.logf("[跳过] %s（已存在）", result.FileName)
		default:
			p.mu.Lock()
			p.bytesDone = result.Size
			p.mu.Unlock()
			_ = p.bar.Set64(result.Size)
			_ = p.bar.Finish()
		}
		return
	}

	switch {
	case result.Err != nil:
		p.logf("[失败] %s：%v", result.FileName, result.Err)
		p.advance(result.Size - last)
	case result.Skipped:
		p.logf("[跳过] %s（已存在）", result.FileName)
		p.advance(result.Size)
	default:
		p.advance(result.Size - last)
	}
}

// retry 在进度条上方提示正在重试，避免看起来像卡住或直接跳过。
func (p *downloadProgress) retry(job mtproto.MediaJob, attempt int, err error) {
	msg := []rune(err.Error())
	if len(msg) > 100 {
		msg = append(msg[:100], '…')
	}
	p.logf("[重试] %s（第 %d 次）：%s", job.FileName, attempt, string(msg))
}

// finish 停止描述刷新并把进度条补满。
func (p *downloadProgress) finish() {
	p.close()

	if p.perFile {
		p.bar.Describe(fmt.Sprintf("[green]✔[reset] 处理结束 · %d 个文件", p.files))
		_ = p.bar.Finish()
		fmt.Fprint(os.Stderr, "\n")
		return
	}

	p.mu.Lock()
	p.bytesDone = p.total
	p.mu.Unlock()
	p.bar.Describe(fmt.Sprintf("[green]✔[reset] 处理结束 · %s", utils.FormatBytes(p.total)))
	_ = p.bar.Finish()
	fmt.Fprint(os.Stderr, "\n")
}

// abort 在连接级失败时清掉进度条，保留当前输出。
func (p *downloadProgress) abort() {
	p.close()
	_ = p.bar.Clear()
	_ = p.bar.Exit()
	fmt.Fprint(os.Stderr, "\n")
}

// close 停止 spinner；可重复调用。
func (p *downloadProgress) close() {
	p.stopOnce.Do(func() {
		close(p.stop)
		<-p.stopped
	})
}

// logf 在进度条上方打印一行，避免与进度条重绘互相覆盖。
func (p *downloadProgress) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = progressbar.Bprintf(p.bar, "\r%s\r%s\n", strings.Repeat(" ", stderrWidth()), msg)
}

// stderrWidth 返回终端宽度，用于擦除整行；拿不到时退回 80。
func stderrWidth() int {
	if width, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && width > 0 {
		return width
	}
	return 80
}

// formatETA 把剩余时间格式化成中文。
func formatETA(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%d 分 %d 秒", minutes, seconds%60)
	}
	return fmt.Sprintf("%d 小时 %d 分", minutes/60, minutes%60)
}

func progressKey(job mtproto.MediaJob) string {
	return job.ChatTitle + "\x00" + job.FileName
}
