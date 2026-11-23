package manager

import (
	"context"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestDownloadLimiter 覆盖上限、运行时调整与取消。
func TestDownloadLimiter(t *testing.T) {
	limiter := newDownloadLimiter(2)
	ctx := context.Background()

	if !limiter.acquire(ctx) || !limiter.acquire(ctx) {
		t.Fatal("上限内应能获取并发位")
	}

	// 超过上限时等待；ctx 超时后返回 false 且不占位。
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	if limiter.acquire(waitCtx) {
		t.Error("超过上限不应获取成功")
	}
	cancel()

	limiter.release()
	if !limiter.acquire(ctx) {
		t.Fatal("释放后应能再次获取")
	}

	// 调小上限不影响已运行的任务，但新的获取要等待。
	limiter.setLimit(1)
	waitCtx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
	if limiter.acquire(waitCtx) {
		t.Error("上限为 1 时不应再获取")
	}
	cancel()

	limiter.release()
	limiter.release()
	limiter.setLimit(1)
	if !limiter.acquire(ctx) {
		t.Error("全部释放后应能获取")
	}
	limiter.release()

	// 取消后的 ctx 不再获取。
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if limiter.acquire(cancelled) {
		t.Error("已取消的 ctx 不应获取成功")
	}
}

// TestClampValues 确认并发数收敛到合法区间。
func TestClampValues(t *testing.T) {
	if got := clampWorkers(0); got != 1 {
		t.Errorf("clampWorkers(0) = %d", got)
	}
	if got := clampWorkers(99); got != MaxWorkers {
		t.Errorf("clampWorkers(99) = %d", got)
	}
	if got := clampThreads(0); got != 1 {
		t.Errorf("clampThreads(0) = %d", got)
	}
	if got := clampThreads(99); got != 8 {
		t.Errorf("clampThreads(99) = %d", got)
	}
}

// TestDownloadLimits 确认从 settings 读取并行文件数与线程数，读不到时退回启动参数。
func TestDownloadLimits(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	settings := config.NewStore(db)
	if err := settings.Set(config.KeyDownloadWorkers, "3"); err != nil {
		t.Fatal(err)
	}
	if err := settings.Set(config.KeyDownloadThreads, "6"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	manager := New(Config{RootDir: t.TempDir(), Workers: 2})
	workers, threads := manager.downloadLimits()
	if workers != 3 || threads != 6 {
		t.Errorf("downloadLimits = %d/%d，期望 3/6", workers, threads)
	}

	// 配置读不到（空库）时用配置项的默认值（1 个文件 / 4 个线程）。
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	manager = New(Config{RootDir: t.TempDir(), Workers: 2})
	workers, threads = manager.downloadLimits()
	if workers != 1 || threads != DefaultThreads {
		t.Errorf("空库 downloadLimits = %d/%d，期望 1/%d", workers, threads, DefaultThreads)
	}
}
