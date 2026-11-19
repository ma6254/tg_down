package accountinfo

import (
	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/cmd/account/internal/ids"
	"github.com/ma6254/tg_down/internal/utils"
)

const defaultNetworkTimeout = utils.DefaultNetworkTimeout

var (
	openDatabase = utils.OpenDatabase
	resolveProxy = utils.ResolveProxy

	maskPhone      = utils.MaskPhone
	formatTime     = utils.FormatTime
	orDash         = utils.OrDash
	describeDevice = utils.DescribeDevice
	parseAccountID = ids.Parse
)

// NewCommand 返回 info 子命令。
func NewCommand() *cobra.Command {
	return accountInfoCmd
}
