package account

import (
	"github.com/spf13/cobra"

	accountinfo "github.com/ma6254/tg_down/cmd/account/info"
	accountlist "github.com/ma6254/tg_down/cmd/account/list"
	"github.com/ma6254/tg_down/cmd/account/login"
	"github.com/ma6254/tg_down/cmd/account/logout"
	accountswitch "github.com/ma6254/tg_down/cmd/account/switch"
)

// NewCommand 返回 account 子命令组，由根命令挂载。
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "管理 Telegram 账号",
		Long: `管理接入的 Telegram 账号。

  tg_down account login --kind mtproto --phone +8613800000000
  tg_down account login --kind botapi --token 123456789:AA...
  tg_down account list
  tg_down account info 1
  tg_down account logout 1

账号存储在本地 SQLite 中；MTProto 的 session 与 Bot Token 都属于敏感数据，
不会通过接口回传，也不会写进日志。`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		login.NewCommand(),
		accountlist.NewCommand(),
		accountinfo.NewCommand(),
		accountswitch.NewCommand(),
		logout.NewCommand(),
	)
	return cmd
}
