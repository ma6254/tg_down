// Package cmd 是命令行入口：只负责参数解析、校验与进程生命周期，
// 业务逻辑下沉到 internal/ 各层。
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/cmd/account"
	configcmd "github.com/ma6254/tg_down/cmd/config"
	"github.com/ma6254/tg_down/cmd/download"
	"github.com/ma6254/tg_down/cmd/serve"
	"github.com/ma6254/tg_down/internal/build"
)

var (
	opts    = newOptions() // 服务参数，root 与 serve 共用
	workDir string         // 启动前切换的工作目录

	// serveCmd 既是 serve 子命令，也是不带子命令时根命令的默认行为：
	// 两处共用同一份命令定义与同一个 opts，只额外在根上注册一个隐藏的 --listen。
	serveCmd = serve.NewCommand(serve.Deps{
		// --listen 绑定到根命令的 Options，根上另有一份隐藏的同名 flag。
		BindListen: opts.bindServe,
		WorkDir:    func() string { return workDir },
		Validate:   opts.validate,
		Options:    opts.serveOptions,
	})
)

// rootCmd 是根命令：不带子命令时直接启动服务。
var rootCmd = &cobra.Command{
	Use:   "tg_down",
	Short: "Telegram 下载工具",
	Long: `tg_down 把 Telegram 里能看到的媒体下载到本地磁盘。

默认启动常驻服务，同时提供 /api 接口与 Web 界面：
  tg_down                          等价于 tg_down serve
  tg_down serve --listen :22429    指定监听地址

服务默认只监听回环地址（127.0.0.1）；绑定到非回环地址时必须提供
--auth-token，否则拒绝启动。`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          serveCmd.RunE,
}

// Execute 是程序入口，由 main 调用。
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&workDir, "workdir", "", "启动前切换的工作目录，数据与下载路径都相对于它解析")
	opts.bind(rootCmd.PersistentFlags())

	// 根命令不带子命令时等价于 serve，因此也要能接受 --listen。
	// 这里注册成隐藏 flag：既不污染全局参数帮助，子命令（本地 flag 不继承）
	// 也拿不到它，--listen 只出现在 serve 自己的帮助里。
	opts.bindServe(rootCmd.Flags())
	_ = rootCmd.Flags().MarkHidden("listen")

	rootCmd.AddCommand(serveCmd, account.NewCommand(), configcmd.NewCommand(),
		download.NewCommand(download.Defaults{
			DownloadDir:    func() string { return opts.DownloadDir },
			MaxConcurrency: func() int { return opts.MaxConcurrency },
		}), versionCmd)

	// 让帮助里的命令列表显示别称（如 list 的 ls）。
	applyUsageTemplate(rootCmd)

	// rootCmd 的版本信息交给 version 子命令，--version 直接复用。
	rootCmd.Version = build.Version
}
