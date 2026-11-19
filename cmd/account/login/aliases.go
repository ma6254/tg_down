package login

import "github.com/ma6254/tg_down/internal/utils"

// 共享能力别名：命令层直接使用 internal 里的实现。
const defaultNetworkTimeout = utils.DefaultNetworkTimeout

var (
	openDatabase = utils.OpenDatabase
	resolveProxy = utils.ResolveProxy

	maskPhone      = utils.MaskPhone
	digitsOnly     = utils.DigitsOnly
	formatTime     = utils.FormatTime
	describeDevice = utils.DescribeDevice
	accountAttrs   = utils.AccountAttrs
)
