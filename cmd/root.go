// Package cmd 是命令行入口：只负责参数解析、校验与进程生命周期，
// 业务逻辑下沉到 internal/ 各层。
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/cmd/account"
	configcmd "github.com/ma6254/tg_down/cmd/config"
	"github.com/ma6254/tg_down/internal/build"
)

var (
	opts    = newOptions() // 服务参数，root 与 serve 共用
	workDir string         // 启动前切换的工作目录
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
	RunE:          runServe,
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

	rootCmd.AddCommand(serveCmd, account.NewCommand(), configcmd.NewCommand(), versionCmd)

	// 让帮助里的命令列表显示别称（如 list 的 ls）。
	applyUsageTemplate(rootCmd)

	// rootCmd 的版本信息交给 version 子命令，--version 直接复用。
	rootCmd.Version = build.Version
}
