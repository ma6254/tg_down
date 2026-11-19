package login

import (
	"github.com/spf13/cobra"
)

// NewCommand 返回 login 子命令。
func NewCommand() *cobra.Command {
	return accountLoginCmd
}
