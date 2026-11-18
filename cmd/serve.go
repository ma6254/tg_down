package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/build"
)

// errServeNotImplemented 表示 HTTP 服务本体尚未接入，避免"启动成功但什么都没发生"。
var errServeNotImplemented = errors.New("HTTP 服务尚未接入（M1 里程碑）：当前仅完成 CLI 参数定义与校验")

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

// runServe 完成工作目录切换、参数校验与启动信息输出。
func runServe(_ *cobra.Command, _ []string) error {
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			return fmt.Errorf("切换工作目录 %q: %w", workDir, err)
		}
	}
	if err := opts.validate(); err != nil {
		return err
	}
	printStartupInfo()
	return errServeNotImplemented
}

// printStartupInfo 打印生效配置，便于确认命令行参数是否按预期覆盖。
func printStartupInfo() {
	cfgSource := "内置默认值"
	if cfgPath != "" {
		cfgSource = cfgPath + "（配置文件尚未接入，暂不读取）"
	}

	fmt.Printf(`
  ┌──────────────────────────────────────────────┐
  │   tg_down · Telegram 下载工具                 │
  └──────────────────────────────────────────────┘
  版本      %s（commit %s，built %s）
  配置      %s
  监听      http://%s
  下载目录  %s
  并发      %d
  鉴权      %s
  日志级别  %s
`,
		build.Version, build.Commit, build.BuildTime,
		cfgSource,
		opts.Listen,
		opts.DownloadDir,
		opts.MaxConcurrency,
		opts.authMode(),
		opts.LogLevel,
	)
}
