package logout

import (
	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/utils"
)

var openDatabase = utils.OpenDatabase

// NewCommand 返回 logout 子命令。
func NewCommand() *cobra.Command {
	return accountLogoutCmd
}
