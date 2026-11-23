package manager

import (
	"context"
	"sync"
	"time"

	"github.com/ma6254/tg_down/internal/download/runner"
)

// limiterPollInterval 是等待空位时的轮询间隔。
//
// 不用 sync.Cond：暂停/停机时 ctx 会取消，而 Cond 无法 select context。
// 20ms 的轮询延迟对下载任务的启动时间可以忽略。
const limiterPollInterval = 20 * time.Millisecond

// downloadLimiter 限制同时执行的任务数，并允许运行时调整上限。
type downloadLimiter struct {
	mu     sync.Mutex
	limit  int
	active int
}

// newDownloadLimiter 创建一个限流器；非法上限会被收敛到 1..MaxWorkers。
func newDownloadLimiter(limit int) *downloadLimiter {
	return &downloadLimiter{limit: clampWorkers(limit)}
}

// setLimit 更新上限；正在运行的任务不受影响，只在下次 acquire 生效。
func (l *downloadLimiter) setLimit(limit int) {
	limit = clampWorkers(limit)
	l.mu.Lock()
	l.limit = limit
	l.mu.Unlock()
}

// acquire 占一个并发位；ctx 取消或等待超时时返回 false。
func (l *downloadLimiter) acquire(ctx context.Context) bool {
	for {
		if err := ctx.Err(); err != nil {
			return false
		}
		l.mu.Lock()
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return true
		}
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return false
		case <-time.After(limiterPollInterval):
		}
	}
}

// release 归还并发位。
func (l *downloadLimiter) release() {
	l.mu.Lock()
	if l.active > 0 {
		l.active--
	}
	l.mu.Unlock()
}

// clampWorkers 把并行文件数收敛到 1..MaxWorkers。
func clampWorkers(value int) int {
	if value < 1 {
		return 1
	}
	if value > MaxWorkers {
		return MaxWorkers
	}
	return value
}

// clampThreads 把单文件线程数收敛到 1..runner.MaxThreads。
func clampThreads(value int) int {
	if value < 1 {
		return 1
	}
	if value > runner.MaxThreads {
		return runner.MaxThreads
	}
	return value
}
