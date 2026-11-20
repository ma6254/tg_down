// Package download 提供 download（别称 dl）子命令。
//
// 命令负责参数、输出与退出码；链接解析与下载执行在 internal 各层。
package download

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewCommand 返回 download 子命令，由根命令挂载。
//
// defaults 让下载目录与并发数复用根命令的全局参数，而不是再定义一份。
func NewCommand(defaults Defaults) *cobra.Command {
	opts := &options{defaults: defaults}

	cmd := &cobra.Command{
		Use:     "download",
		Aliases: []string{"dl"},
		Short:   "按 Telegram 消息链接下载媒体",
		Long: `按 Telegram 消息链接下载媒体。

链接用 -u/--url 传入，可重复，覆盖频道帖子、讨论组评论与话题：
  tg_down download -u https://t.me/somechannel/123
  tg_down download -u "https://t.me/somechannel/123?comment=456" -u https://t.me/c/1234567890/42

默认下载到 --download-dir（./downloads），并发取 --max-concurrency；
已存在且大小一致的文件会跳过（--force 重新下载），中断留下的 .part 自动续传。
--flat 不按会话建子目录，--threads 控制单文件并行分块数（默认 8）；
每个文件使用 8 条连接（gotd 连接池），以充分利用高延迟代理的带宽。

--expand-text-links 会把消息正文里的文字链接（text_link）展开成待下载的消息，
只展开一层，最多 200 条；--export-links 把查到媒体的消息链接写进 txt：
  tg_down download -u https://t.me/JPD233/2522 --expand-text-links --export-links videos.txt

--dry-run 只解析并预览，不写文件。

多个链接也可先写成多行文件，再用管道传入。`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("链接请用 -u/--url 传入，例如 tg_down download -u %s", args[0])
			}
			if len(opts.URLs) == 0 {
				return cmd.Help()
			}
			return runDownload(cmd, opts)
		},
	}

	opts.bind(cmd.Flags())
	return cmd
}
