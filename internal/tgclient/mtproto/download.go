package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	downloadsvc "github.com/ma6254/tg_down/internal/download"
)

// downloadPartSize 是单次 upload.getFile 的字节数。Telegram 上限 1 MiB；
// 比 gotd 默认的 512 KiB 大一倍，可减少高延迟线路上的请求次数。
const downloadPartSize = 1024 * 1024

// maxDownloadAttempts 是单个文件的最大尝试次数（首次 + 3 次重试）。
// 代理偶尔会重置长连接，重建连接池后从 .part 继续即可。
const maxDownloadAttempts = 4

// MediaJob 是一个待下载的媒体文件。
type MediaJob struct {
	ChatTitle string
	FileName  string
	Size      int64
	Media     *Media
}

// DownloadOptions 控制下载行为；回调用于 CLI 输出进度。
type DownloadOptions struct {
	RootDir     string
	Concurrency int
	Threads     int
	// PoolSize 是每个文件所在 DC 的连接池大小；多条 TCP 连接可突破
	// 单连接在高延迟代理下的吞吐上限。
	PoolSize int
	Force    bool
	Flat     bool

	// OnStart 在真正开始传输前调用；plan.Offset > 0 表示续传。
	OnStart func(job MediaJob, plan downloadsvc.Plan)
	// OnProgress 在每块数据写完后调用；callback 可能并发触发。
	OnProgress func(job MediaJob, done, total int64)
	// OnRetry 在单文件传输失败、即将重建连接重试时调用。
	OnRetry func(job MediaJob, attempt int, err error)
	// OnDone 在每个文件结束时调用（成功、跳过或失败都算），
	// 进度条实现据此完成或中止对应的条。
	OnDone func(result DownloadResult)
}

// DownloadResult 是单个文件的下载结果。
type DownloadResult struct {
	ChatTitle string
	FileName  string
	Path      string
	Size      int64
	Skipped   bool
	Resumed   bool
	Bytes     int64
	Err       error
}

// DownloadMedia 用多连接池并发下载多个媒体文件，支持 .part 断点续传。
//
// 单文件失败只写进对应结果的 Err，不影响其它文件。
func (s Session) DownloadMedia(ctx context.Context, jobs []MediaJob, opts DownloadOptions) ([]DownloadResult, error) {
	opts = normalizeDownloadOptions(opts)

	optsLogin := s.options()
	if optsLogin.APIID <= 0 || optsLogin.APIHash == "" {
		return nil, errors.New("缺少 api 凭据")
	}
	if optsLogin.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(optsLogin.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", optsLogin.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(optsLogin)
	if err != nil {
		return nil, err
	}

	results := make([]DownloadResult, len(jobs))
	var planMu sync.Mutex

	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return errors.New("session 已失效，请重新执行 account login")
		}

		// 每个文件所在的 DC 建一个多连接池；不同文件可复用同一 DC 的连接。
		pools := newDownloadPools(client, int64(opts.PoolSize))
		defer pools.close()

		// 任一文件在重试后仍失败时停止整个队列，避免跳过它继续下后面的文件。
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var (
			stopOnce sync.Once
			stopErr  error
		)

		work := make(chan int, len(jobs))
		for i := range jobs {
			work <- i
		}
		close(work)

		var wg sync.WaitGroup
		wg.Add(opts.Concurrency)
		for worker := 0; worker < opts.Concurrency; worker++ {
			go func() {
				defer wg.Done()
				for index := range work {
					if runCtx.Err() != nil {
						return
					}
					result := downloadOne(runCtx, pools, jobs[index], opts, &planMu)
					results[index] = result
					if result.Err != nil && !errors.Is(result.Err, context.Canceled) {
						stopOnce.Do(func() {
							stopErr = result.Err
							cancel()
						})
						return
					}
				}
			}()
		}
		wg.Wait()
		if stopErr != nil {
			return stopErr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// downloadPools 按 DC 懒创建多连接 invoker，所有文件共享。
//
// 单条 TCP 连接在高延迟代理下会被窗口/丢包限制，tdl 等工具默认用 8 条
// 连接（--pool）才跑满线路；这里用 gotd 的 Pool/DC 实现同样效果。
type downloadPools struct {
	client *telegram.Client
	size   int64

	mu    sync.Mutex
	pools map[int]telegram.CloseInvoker
}

func newDownloadPools(client *telegram.Client, size int64) *downloadPools {
	return &downloadPools{
		client: client,
		size:   size,
		pools:  make(map[int]telegram.CloseInvoker),
	}
}

func (p *downloadPools) invoker(ctx context.Context, dc int) (tg.Invoker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	current := p.client.Config().ThisDC
	if dc <= 0 {
		dc = current
	}
	if inv, ok := p.pools[dc]; ok {
		return inv, nil
	}

	var (
		inv telegram.CloseInvoker
		err error
	)
	if dc == current {
		inv, err = p.client.Pool(p.size)
	} else {
		inv, err = p.client.DC(ctx, dc, p.size)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 DC %d: %w", dc, err)
	}
	p.pools[dc] = inv
	return inv, nil
}

func (p *downloadPools) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, inv := range p.pools {
		_ = inv.Close()
	}
}

// reset 关闭并丢弃某个 DC 的连接池，下次请求会重新建连。
// 代理重置 TCP 连接后，旧池里的连接可能已全部失效，重试前必须换新池。
func (p *downloadPools) reset(dc int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if dc <= 0 {
		dc = p.client.Config().ThisDC
	}
	if inv, ok := p.pools[dc]; ok {
		_ = inv.Close()
		delete(p.pools, dc)
	}
}

// retryInvoker 在每个 upload.getFile RPC 上做透明重试：
// 代理重置单条连接时，换连接池里的其它连接重发当前分块，
// 而不是让整份文件失败。tdl 默认也带 retry 中间件。
type retryInvoker struct {
	pools    *downloadPools
	dc       int
	attempts int
}

func (r retryInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	var lastErr error
	for attempt := 1; attempt <= r.attempts; attempt++ {
		invoker, err := r.pools.invoker(ctx, r.dc)
		if err == nil {
			err = invoker.Invoke(ctx, input, output)
			if err == nil {
				return nil
			}
		}

		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == r.attempts {
			break
		}
		// 不在这里关闭整个连接池，避免影响其它并发分块；
		// 连接池内部会剔除失效连接，文件级失败时再整体重建。
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	return lastErr
}

// downloadOne 完成单个文件：规划路径、续传偏移、传输与重命名。
//
// 代理偶尔会重置 TCP 连接（wsasend: connection aborted）。此时关闭并重建
// 该 DC 的连接池，从 .part 续传重试，而不是直接跳过文件。
func downloadOne(ctx context.Context, pools *downloadPools, job MediaJob, opts DownloadOptions, planMu *sync.Mutex) (result DownloadResult) {
	result = DownloadResult{ChatTitle: job.ChatTitle, FileName: job.FileName}
	defer func() {
		if opts.OnDone != nil {
			opts.OnDone(result)
		}
	}()

	if job.Media == nil {
		result.Err = errors.New("没有可下载的媒体")
		return result
	}

	size := job.Size
	if size <= 0 {
		size = job.Media.Size
	}
	result.Size = size
	location, err := job.Media.fileLocation()
	if err != nil {
		result.Err = err
		return result
	}

	resumedFrom := int64(0)
	for attempt := 1; attempt <= maxDownloadAttempts; attempt++ {
		// --force 只在首次尝试时从零开始；重试必须沿用 .part 续传。
		force := opts.Force && attempt == 1

		// 同一目录下可能并发规划同名文件，串行化避免选中同一个目标。
		planMu.Lock()
		plan, err := downloadsvc.NewPlan(opts.RootDir, job.ChatTitle, job.FileName, size, opts.Flat, force)
		planMu.Unlock()
		if err != nil {
			result.Err = err
			return result
		}
		if plan.Skip {
			result.Skipped = true
			result.Path = plan.FinalPath
			return result
		}
		if attempt == 1 {
			resumedFrom = plan.Offset
			if opts.OnStart != nil {
				opts.OnStart(job, plan)
			}
		}

		if plan.Offset > 0 {
			// 丢掉失败尝试留下的未对齐尾巴，避免续传后文件体积虚高。
			if err := os.Truncate(plan.PartPath, plan.Offset); err != nil && !errors.Is(err, os.ErrNotExist) {
				result.Err = fmt.Errorf("准备续传文件: %w", err)
				return result
			}
		} else if force {
			_ = os.Remove(plan.PartPath)
		}

		invoker := retryInvoker{
			pools:    pools,
			dc:       job.Media.DCID,
			attempts: 5,
		}

		builder := downloader.NewDownloader().
			WithPartSize(downloadPartSize).
			Download(tg.NewClient(invoker), location).
			WithSize(size).
			WithThreads(opts.Threads)
		if plan.Offset > 0 {
			builder = builder.WithOffset(plan.Offset)
		}
		if opts.OnProgress != nil {
			builder = builder.WithProgress(func(done, total int64) {
				opts.OnProgress(job, done, total)
			})
		}

		_, transferErr := builder.ToPath(ctx, plan.PartPath)
		if transferErr == nil {
			if renameErr := os.Rename(plan.PartPath, plan.FinalPath); renameErr != nil {
				transferErr = fmt.Errorf("重命名到最终文件: %w", renameErr)
			} else {
				result.Path = plan.FinalPath
				result.Resumed = resumedFrom > 0
				result.Bytes = size
				return result
			}
		}

		if ctx.Err() != nil {
			result.Err = ctx.Err()
			return result
		}
		if attempt < maxDownloadAttempts {
			// 旧连接池可能已整体失效，丢弃后下一次尝试重新建连。
			pools.reset(job.Media.DCID)
			if opts.OnRetry != nil {
				opts.OnRetry(job, attempt, transferErr)
			}
			if waitErr := waitRetry(ctx); waitErr != nil {
				result.Err = waitErr
				return result
			}
			continue
		}

		result.Err = fmt.Errorf("下载 %s（已尝试 %d 次）: %w",
			job.FileName, attempt, friendlyError(transferErr))
		pools.reset(job.Media.DCID)
		return result
	}
	return result
}

// waitRetry 在重建连接前短暂等待，给本地代理释放旧连接的时间。
func waitRetry(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

// normalizeDownloadOptions 收敛并发与线程数，避免非法值。
func normalizeDownloadOptions(opts DownloadOptions) DownloadOptions {
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	if opts.Concurrency > 8 {
		opts.Concurrency = 8
	}
	if opts.Threads < 1 {
		opts.Threads = 1
	}
	if opts.Threads > 8 {
		opts.Threads = 8
	}
	if opts.PoolSize < 1 {
		opts.PoolSize = 8
	}
	if opts.PoolSize > 8 {
		opts.PoolSize = 8
	}
	if opts.RootDir == "" {
		opts.RootDir = "downloads"
	}
	return opts
}
