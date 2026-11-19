package accountswitch

import (
	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/cmd/account/internal/ids"
	"github.com/ma6254/tg_down/internal/utils"
)

var (
	openDatabase   = utils.OpenDatabase
	parseAccountID = ids.Parse
)

// NewCommand 返回 switch 子命令。
func NewCommand() *cobra.Command {
	return accountSwitchCmd
}
