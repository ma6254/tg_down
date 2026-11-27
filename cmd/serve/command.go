// Package serve 提供 serve 子命令，也是不带子命令时的默认行为。
//
// 命令只负责进程生命周期、参数校验与启动横幅；HTTP 路由与处理在
// internal/server，这里把 internal 各层的实现接到 server.Config 上。
package serve

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ma6254/tg_down/internal/build"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
	"github.com/ma6254/tg_down/webembed"
)

// serveShutdownTimeout 是收到退出信号后等待在途请求结束的上限。
const serveShutdownTimeout = 10 * time.Second

// Options 是 serve 启动需要的参数快照，由根命令在参数解析完成后提供。
type Options struct {
	Listen         string // HTTP 监听地址
	AuthToken      string // 非回环监听时的 Bearer token
	DownloadDir    string // 下载根目录
	MaxConcurrency int    // 并发下载数
	LogLevel       string // debug / info / warn / error
	AuthMode       string // 鉴权状态的可读描述，供启动横幅使用
}

// Deps 是 serve 依赖根命令提供的能力。
//
// 一律用函数而不是值：参数要等 Execute 时才解析完，serve 启动前才能读到最终值。
type Deps struct {
	// BindListen 在 serve 自己的命令行上注册 --listen。
	// 根命令另注册一份隐藏的 --listen：不带子命令时同样要能指定监听地址。
	BindListen func(*pflag.FlagSet)
	// WorkDir 返回启动前要切换的工作目录，空表示不切换。
	WorkDir func() string
	// Validate 校验并归一化根命令参数，必须在启动服务前调用。
	Validate func() error
	// Options 返回参数快照，供侦听、下载管理器与启动横幅使用。
	Options func() Options
}

// command 持有注入的依赖，用于构造命令并执行。
type command struct {
	deps Deps
}

// NewCommand 返回 serve 子命令，由根命令挂载。
func NewCommand(deps Deps) *cobra.Command {
	c := &command{deps: deps}
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "启动常驻 HTTP 服务（默认子命令）",
		Long: `启动常驻 HTTP 服务，同时提供 /api 接口与 Web 界面。

服务默认只监听 127.0.0.1:22429；绑定到非回环地址时必须提供
--auth-token，否则拒绝启动。`,
		Args: cobra.NoArgs,
		RunE: c.run,
	}
	deps.BindListen(cmd.Flags())
	return cmd
}

// run 完成工作目录切换、参数校验，然后侦听并阻塞到进程被要求退出。
func (c *command) run(cmd *cobra.Command, _ []string) error {
	if dir := c.deps.WorkDir(); dir != "" {
		if err := os.Chdir(dir); err != nil {
			return fmt.Errorf("切换工作目录 %q: %w", dir, err)
		}
	}
	if err := c.deps.Validate(); err != nil {
		return err
	}
	opts := c.deps.Options()

	// 先完成建表/迁移再监听：并发首个请求各自 OpenDatabase 会同时 AutoMigrate，
	// 提前初始化可以避免迁移竞态，也能让数据库错误在启动阶段暴露。
	if err := utils.PrepareDatabase(); err != nil {
		return err
	}

	// 历史数据修正：旧版本把覆盖开关计入去重 hash，同内容可能留下两行。
	if err := normalizeDownloadHistory(); err != nil {
		fmt.Fprintf(os.Stderr, "规范化下载历史失败（继续启动）: %v\n", err)
	}
	// 把旧草稿里的开关迁移到 settings，并确保下载相关配置存在。
	if err := ensureDownloadSettings(opts.MaxConcurrency); err != nil {
		fmt.Fprintf(os.Stderr, "初始化下载设置失败（继续启动）: %v\n", err)
	}
	// 清理长期未用的消息缓存；失败不阻止启动。
	if err := pruneMessageCache(); err != nil {
		fmt.Fprintf(os.Stderr, "清理消息缓存失败（继续启动）: %v\n", err)
	}

	// SSE 广播器：下载管理器往这里发事件，/api/v1/events 从这里订阅。
	hub := server.NewEventHub()

	// 下载任务管理器：worker 在后台执行队列，状态落库，重启后残留任务标记为暂停。
	mgr := newDownloadManager(opts.DownloadDir, opts.MaxConcurrency, hub)
	if err := mgr.Start(); err != nil {
		return fmt.Errorf("初始化下载任务: %w", err)
	}

	// 先显式侦听再启动服务：端口占用等错误在启动阶段就能给出明确提示，
	// 而不是等进程跑起来后才从 Serve 的返回值里发现。
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return listenError(opts.Listen, err)
	}
	defer ln.Close()

	webFS := embeddedWebFS()
	srv := server.New(server.Config{
		Addr:  opts.Listen,
		Token: opts.AuthToken,
		// 内嵌前端：默认构建通过 webdist 标签打进二进制；--no-web 时回退占位页。
		Web: server.WebConfig{FS: webFS},
		// 登录探测：account login --probe 的 HTTP 版。
		ProbeLogin: probeLogin,
		// 账号列表：account list 的 HTTP 版。
		ListAccounts: listAccounts,
		// 切换当前账号：account switch <id> 的 HTTP 版。
		SelectAccount: selectAccount,
		// 账号详情：account info <id> 的 HTTP 版。
		GetAccount: getAccount,
		// 在线资料：account info <id> --live 的 HTTP 版（联网查询；首次会补写 telegram_id）。
		GetAccountLive: getAccountLive,
		// 账号头像：磁盘缓存，过期后联网刷新。
		GetAccountAvatar: getAccountAvatar,
		// 退出登录：吊销 Telegram session 并删除本地记录。
		LogoutAccount: logoutAccount,
		// 删除账号：只删本地记录，不吊销 Telegram 会话（桌面导入账号用）。
		DeleteAccount: deleteAccount,
		// 链接解析：download -u <链接> 的 HTTP 版（联网查询，不落库）。
		PreviewLinks: previewLinks,
		// 评论区：读取频道帖子关联讨论组里的评论（联网查询，不落库）。
		GetComments: getComments,
		// 系统配置：config list / set / unset 的 HTTP 版。
		GetSettings:    getSettings,
		UpdateSettings: updateSettings,
		GetCacheStats:  cacheStats,
		ClearCache:     clearCache,
		// 下载任务：创建后由后台 worker 执行，进度通过 SSE 推送。
		CreateDownloads:         createDownloads(mgr),
		ListDownloads:           listDownloads(mgr),
		GetDownloadStats:        downloadStats(mgr),
		GetDownloadSpeedHistory: downloadSpeedHistory(mgr),
		ResetDownloadSpeedStats: resetDownloadSpeedStats(mgr),
		GetDownload:             getDownload(mgr),
		ControlDownload:         controlDownload(mgr),
		DeleteDownload: func(_ context.Context, id uint, deleteFile bool) error {
			return mapDownloadError(mgr.Remove(id, deleteFile))
		},
		// 下载页草稿与历史：输入自动保存、最近解析批次回填。
		GetDownloadDraft:        getDownloadDraft,
		SaveDownloadDraft:       saveDownloadDraft,
		ListDownloadHistory:     listDownloadHistory,
		GetDownloadHistory:      getDownloadHistory,
		RecordDownloadHistory:   recordDownloadHistory,
		DeleteDownloadHistory:   deleteDownloadHistory,
		ClearDownloadHistory:    clearDownloadHistory,
		GetDownloadHistoryCover: getDownloadHistoryCover,
		// 实时事件与任务子资源：前者由 hub 直接提供，后者按任务按需生成。
		EventHub:             hub,
		GetDownloadThumbnail: downloadThumbnail(mgr),
		GetDownloadFile:      downloadFile(mgr),
		// 前端接入方式随 Web 构建产物一起接入（proxy / dir / embed）。
	})

	addr := ln.Addr().String()
	printStartupInfo(addr, opts, webFS != nil)
	return serveUntilSignal(cmd.Context(), srv, ln, addr, mgr.Stop, closeTelegram)
}

// embeddedWebFS 返回内嵌进二进制的前端产物；未用 webdist 标签构建时为 nil。
func embeddedWebFS() fs.FS {
	dist, ok := webembed.Dist()
	if !ok {
		return nil
	}
	return dist
}

// serveUntilSignal 运行 HTTP 服务，收到 Ctrl+C 或 SIGTERM 后优雅关闭。
//
// stops 是额外需要关闭的后台组件（如下载管理器），在 HTTP 优雅关闭前调用。
// 收到信号时会打印信号类型；再次按 Ctrl+C 恢复默认行为（立即退出）。
func serveUntilSignal(ctx context.Context, srv *server.Server, ln net.Listener, addr string, stops ...func(context.Context)) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		// 第一个信号进入优雅关闭；注销后第二个 Ctrl+C 回到默认的强制终止。
		signal.Stop(sigCh)
		fmt.Printf("\n收到 %s，正在关闭 %s ...\n", signalName(sig), addr)
	case <-ctx.Done():
		fmt.Printf("\n正在关闭 %s ...\n", addr)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	for _, stopFn := range stops {
		stopFn(shutdownCtx)
	}
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭超时（%s）: %w", serveShutdownTimeout, err)
	}
	fmt.Println("已退出")
	return nil
}

// closeTelegram 关闭常驻的 MTProto 连接，避免进程退出时留下后台连接。
func closeTelegram(context.Context) {
	mtproto.CloseConnections()
}

// signalName 把信号映射成可读名字，便于确认是哪种方式结束了进程。
func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "Ctrl+C（interrupt）"
	case syscall.SIGTERM:
		return "SIGTERM（terminate）"
	default:
		return sig.String()
	}
}

// listenError 把底层网络错误翻译成可操作的提示。
func listenError(addr string, err error) error {
	if utils.IsAddrInUse(err) {
		return fmt.Errorf("监听 %s 失败：端口已被占用（Windows 可用 netstat -ano | findstr :端口 查看占用进程）: %w", addr, err)
	}
	if msg := strings.ToLower(err.Error()); strings.Contains(msg, "forbidden") || strings.Contains(msg, "permission") {
		return fmt.Errorf("监听 %s 失败：端口被系统保留或没有权限（Windows 可用 netsh interface ipv4 show excludedportrange protocol=tcp 查看保留端口段）: %w", addr, err)
	}
	return fmt.Errorf("监听 %s 失败: %w", addr, err)
}

// printStartupInfo 打印生效配置与真实监听地址，便于确认参数是否按预期覆盖。
func printStartupInfo(addr string, opts Options, webEmbedded bool) {
	webLabel := "占位页（未内嵌前端）"
	if webEmbedded {
		webLabel = "内置前端（webdist）"
	}
	fmt.Printf(`
  ┌──────────────────────────────────────────────┐
  │   tg_down · Telegram 下载工具                 │
  └──────────────────────────────────────────────┘
  版本      %s（commit %s，built %s）
  配置      数据库 settings（tg_down config）
  监听      http://%s
  Web 界面  %s
  健康检查  http://%s/api/v1/health
  接口文档  http://%s/swagger/
  下载目录  %s
  并发      %d
  鉴权      %s
  日志级别  %s
`,
		build.Version, build.Commit, build.BuildTime,
		addr,
		webLabel,
		addr,
		addr,
		opts.DownloadDir,
		opts.MaxConcurrency,
		opts.AuthMode,
		opts.LogLevel,
	)
}
