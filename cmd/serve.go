package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/build"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// serveShutdownTimeout 是收到退出信号后等待在途请求结束的上限。
const serveShutdownTimeout = 10 * time.Second

func init() {
	// --listen 只属于 serve：它决定 HTTP 监听地址，其它子命令用不到。
	opts.bindServe(serveCmd.Flags())
}

// serveCmd 启动常驻服务，也是不带子命令时的默认行为。
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "启动常驻 HTTP 服务（默认子命令）",
	Long: `启动常驻 HTTP 服务，同时提供 /api 接口与 Web 界面。

服务默认只监听 127.0.0.1:22429；绑定到非回环地址时必须提供
--auth-token，否则拒绝启动。`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

// runServe 完成工作目录切换、参数校验，然后侦听并阻塞到进程被要求退出。
func runServe(cmd *cobra.Command, _ []string) error {
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			return fmt.Errorf("切换工作目录 %q: %w", workDir, err)
		}
	}
	if err := opts.validate(); err != nil {
		return err
	}

	// 先完成建表/迁移再监听：并发首个请求各自 OpenDatabase 会同时 AutoMigrate，
	// 提前初始化可以避免迁移竞态，也能让数据库错误在启动阶段暴露。
	if err := prepareDatabase(); err != nil {
		return err
	}

	// 先显式侦听再启动服务：端口占用等错误在启动阶段就能给出明确提示，
	// 而不是等进程跑起来后才从 Serve 的返回值里发现。
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return listenError(opts.Listen, err)
	}
	defer ln.Close()

	srv := server.New(server.Config{
		Addr:  opts.Listen,
		Token: opts.AuthToken,
		// 登录探测：account login --probe 的 HTTP 版。
		ProbeLogin: probeLogin,
		// 账号列表：account list 的 HTTP 版。
		ListAccounts: listAccounts,
		// 切换当前账号：account switch <id> 的 HTTP 版。
		SelectAccount: selectAccount,
		// 账号详情：account info <id> 的 HTTP 版。
		GetAccount: getAccount,
		// 在线资料：account info <id> --live 的 HTTP 版（联网查询，不落库）。
		GetAccountLive: getAccountLive,
		// 链接解析：download -u <链接> 的 HTTP 版（联网查询，不落库）。
		PreviewLinks: previewLinks,
		// 媒体缩略图：解析结果里的 token 换取小图。
		GetMediaThumbnail: getMediaThumbnail,
		// 系统配置：config list / set / unset 的 HTTP 版。
		GetSettings:    getSettings,
		UpdateSettings: updateSettings,
		// 前端接入方式随 Web 构建产物一起接入（proxy / dir / embed）。
	})

	addr := ln.Addr().String()
	printStartupInfo(addr)
	return serveUntilSignal(cmd.Context(), srv, ln, addr)
}

// prepareDatabase 在开始监听前完成建表与迁移。
func prepareDatabase() error {
	db, err := utils.OpenDatabase()
	if err != nil {
		return fmt.Errorf("初始化数据库: %w", err)
	}
	if err := config.SyncTypes(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("同步配置类型: %w", err)
	}
	if err := config.SeedDefaults(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("初始化默认配置: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("关闭数据库: %w", err)
	}
	return nil
}

// serveUntilSignal 运行 HTTP 服务，收到 Ctrl+C 或 SIGTERM 后优雅关闭。
func serveUntilSignal(ctx context.Context, srv *server.Server, ln net.Listener, addr string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	fmt.Printf("\n正在关闭 %s ...\n", addr)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	if err := srv.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭超时（%s）: %w", serveShutdownTimeout, err)
	}
	fmt.Println("已退出")
	return nil
}

// wsaEADDRINUSE 是 Windows 的“端口已占用”错误码（10048）。
// Go 只把 syscall.EADDRINUSE 映射为应用错误，不覆盖 WSA 错误码，
// 所以这里单独判断，兼顾 Unix 与 Windows。
const wsaEADDRINUSE = 10048

// listenError 把底层网络错误翻译成可操作的提示。
func listenError(addr string, err error) error {
	if isAddrInUse(err) {
		return fmt.Errorf("监听 %s 失败：端口已被占用（Windows 可用 netstat -ano | findstr :端口 查看占用进程）: %w", addr, err)
	}
	if msg := strings.ToLower(err.Error()); strings.Contains(msg, "forbidden") || strings.Contains(msg, "permission") {
		return fmt.Errorf("监听 %s 失败：端口被系统保留或没有权限（Windows 可用 netsh interface ipv4 show excludedportrange protocol=tcp 查看保留端口段）: %w", addr, err)
	}
	return fmt.Errorf("监听 %s 失败: %w", addr, err)
}

// isAddrInUse 同时识别 Unix 的 EADDRINUSE 与 Windows 的 WSAEADDRINUSE。
func isAddrInUse(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && (int(errno) == wsaEADDRINUSE || errors.Is(errno, syscall.EADDRINUSE)) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "in use") || strings.Contains(msg, "only one usage")
}

// printStartupInfo 打印生效配置与真实监听地址，便于确认参数是否按预期覆盖。
func printStartupInfo(addr string) {
	fmt.Printf(`
  ┌──────────────────────────────────────────────┐
  │   tg_down · Telegram 下载工具                 │
  └──────────────────────────────────────────────┘
  版本      %s（commit %s，built %s）
  配置      数据库 settings（tg_down config）
  监听      http://%s
  健康检查  http://%s/api/v1/health
  接口文档  http://%s/swagger/
  下载目录  %s
  并发      %d
  鉴权      %s
  日志级别  %s
`,
		build.Version, build.Commit, build.BuildTime,
		addr,
		addr,
		addr,
		opts.DownloadDir,
		opts.MaxConcurrency,
		opts.authMode(),
		opts.LogLevel,
	)
}
