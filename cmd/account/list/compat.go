package accountlist

import (
	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/utils"
)

var (
	openDatabase = utils.OpenDatabase
	maskPhone    = utils.MaskPhone
)

// NewCommand 返回 list 子命令。
func NewCommand() *cobra.Command {
	return accountListCmd
}
