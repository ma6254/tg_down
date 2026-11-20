package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestClampOptions 确认并发/线程/连接池被收敛到合法范围。
func TestClampOptions(t *testing.T) {
	zero := Runner{}
	if got := zero.concurrency(); got != 1 {
		t.Errorf("默认并发 = %d，期望 1", got)
	}
	if got := zero.threads(); got != DefaultThreads {
		t.Errorf("默认线程 = %d，期望 %d", got, DefaultThreads)
	}
	if got := zero.poolSize(); got != DefaultPoolSize {
		t.Errorf("默认连接池 = %d，期望 %d", got, DefaultPoolSize)
	}

	over := Runner{Concurrency: 99, Threads: 99, PoolSize: 99}
	if got := over.concurrency(); got != MaxThreads {
		t.Errorf("并发上限 = %d，期望 %d", got, MaxThreads)
	}
	if got := over.threads(); got != MaxThreads {
		t.Errorf("线程上限 = %d，期望 %d", got, MaxThreads)
	}
	if got := over.poolSize(); got != MaxPoolSize {
		t.Errorf("连接池上限 = %d，期望 %d", got, MaxPoolSize)
	}

	under := Runner{Concurrency: -3, Threads: -3, PoolSize: -3}
	if got := under.concurrency(); got != 1 {
		t.Errorf("并发下限 = %d，期望 1", got)
	}
	if got := under.threads(); got != MinThreads {
		t.Errorf("线程下限 = %d，期望 %d", got, MinThreads)
	}
	if got := under.poolSize(); got != DefaultPoolSize {
		t.Errorf("连接池未设置时 = %d，期望默认值 %d", got, DefaultPoolSize)
	}
}

// TestDownloadEmptyJobs 没有任务时不产生任何结果，也不触碰连接。
func TestDownloadEmptyJobs(t *testing.T) {
	results, err := Runner{}.Download(context.Background(), nil)
	if err != nil || results != nil {
		t.Errorf("空任务 = %v, %v", results, err)
	}
}

// TestDownloadCancelled 上下文已取消时不进入传输阶段。
func TestDownloadCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Runner{}.Download(ctx, []mtproto.MediaJob{{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误 = %v，期望 context.Canceled", err)
	}
}
