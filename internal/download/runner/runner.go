// Package runner 是 CLI 与 HTTP 共用的下载执行入口。
//
// 它把「一批 MediaJob + 回调」收敛成一次调用，统一 DownloadOptions 的默认值
// 与取消语义；调用方只在 Hooks 里做展示（CLI）或落库推事件（HTTP）。
package runner

import (
	"context"
	"errors"
	"fmt"

	downloadsvc "github.com/ma6254/tg_down/internal/download"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// 单文件下载的默认参数与取值范围，与 normalizeDownloadOptions 保持一致。
const (
	// DefaultThreads 是未指定线程数时单文件的分块并发数。
	DefaultThreads = 1
	// DefaultPoolSize 是每个文件所在 DC 的连接池大小。
	DefaultPoolSize = 8
	// MinThreads / MaxThreads 限制可配置的分块并发。
	MinThreads = 1
	MaxThreads = 8
	// MinPoolSize / MaxPoolSize 限制可配置的连接池大小。
	MinPoolSize = 1
	MaxPoolSize = 8
)

// ErrNoResult 表示执行完成却没有拿到结果（通常是取消发生在传输开始前）。
var ErrNoResult = errors.New("下载没有产生结果")

// Hooks 是一次下载过程中的回调。
//
// CLI 用它画进度条，HTTP 用它落库并推 SSE；Runner 本身不关心展示方式。
type Hooks struct {
	// OnStart 在真正开始传输前调用；plan.Offset > 0 表示续传。
	OnStart func(job mtproto.MediaJob, plan downloadsvc.Plan)
	// OnProgress 在每块数据写完后调用；可能并发触发。
	OnProgress func(job mtproto.MediaJob, done, total int64)
	// OnRetry 在单文件传输失败、即将重建连接重试时调用。
	OnRetry func(job mtproto.MediaJob, attempt int, err error)
	// OnDone 在每个文件结束时调用（成功、跳过或失败都算）。
	OnDone func(result mtproto.DownloadResult)
}

// Runner 执行一批媒体文件的下载。
//
// 这是 CLI 与 HTTP 共用的执行入口：两个调用方只在 Hooks 里做各自的展示或
// 落库，不再各写一份 DownloadOptions 默认值与结果判定。
// 同一个 Runner 内的多个文件共享一条客户端连接（见 DownloadMedia），
// 因此批量下载应一次传多个 job，而不是每个文件新建一个 Runner。
type Runner struct {
	Session mtproto.Session

	// RootDir 是下载根目录；Flat 为 true 时不按会话建子目录。
	RootDir string
	Flat    bool
	// Concurrency 是同时下载的文件数。
	Concurrency int
	// Threads 是单文件的分块并发数。
	Threads int
	// PoolSize 是每个文件所在 DC 的连接池大小。
	PoolSize int
	// Force 重新下载已存在的文件；Overwrite 直接覆盖同名文件。
	Force     bool
	Overwrite bool

	Hooks Hooks
}

// Download 下载给定的全部文件，返回每个文件的结果。
//
// 单个文件失败只写进对应结果的 Err，不影响其它文件；
// 返回的 error 只表示整批判失败（连接失败、上下文取消）。
func (r Runner) Download(ctx context.Context, jobs []mtproto.MediaJob) ([]mtproto.DownloadResult, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	results, err := r.Session.DownloadMedia(ctx, jobs, mtproto.DownloadOptions{
		RootDir:     r.RootDir,
		Concurrency: r.concurrency(),
		Threads:     r.threads(),
		PoolSize:    r.poolSize(),
		Force:       r.Force,
		Overwrite:   r.Overwrite,
		Flat:        r.Flat,
		OnStart:     r.Hooks.OnStart,
		OnProgress:  r.Hooks.OnProgress,
		OnRetry:     r.Hooks.OnRetry,
		OnDone:      r.Hooks.OnDone,
	})
	if err != nil {
		return nil, err
	}
	// 取消可能在开始传输前生效：这时 DownloadMedia 直接返回空结果，
	// 不能当成成功（调用方要按「已取消」落库）。
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// DownloadOne 下载单个文件并校验结果数量。
func (r Runner) DownloadOne(ctx context.Context, job mtproto.MediaJob) (mtproto.DownloadResult, error) {
	results, err := r.Download(ctx, []mtproto.MediaJob{job})
	if err != nil {
		return mtproto.DownloadResult{}, err
	}
	if len(results) != 1 {
		return mtproto.DownloadResult{}, fmt.Errorf("%w：结果数 %d", ErrNoResult, len(results))
	}
	return results[0], nil
}

func (r Runner) concurrency() int {
	if r.Concurrency < 1 {
		return 1
	}
	if r.Concurrency > MaxThreads {
		return MaxThreads
	}
	return r.Concurrency
}

func (r Runner) threads() int {
	if r.Threads < MinThreads {
		return DefaultThreads
	}
	if r.Threads > MaxThreads {
		return MaxThreads
	}
	return r.Threads
}

func (r Runner) poolSize() int {
	if r.PoolSize < MinPoolSize {
		return DefaultPoolSize
	}
	if r.PoolSize > MaxPoolSize {
		return MaxPoolSize
	}
	return r.PoolSize
}
