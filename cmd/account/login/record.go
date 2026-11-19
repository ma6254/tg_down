package login

import (
	"github.com/ma6254/tg_down/cmd/account/internal/store"
	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// recordAccount 把登录成功的账号登记到账号服务（按 session 路径去重）。
//
// 这里只做「登录参数 → RecordInput」的映射，落库逻辑在 internal/account。
func recordAccount(lo *loginOptions, acc *mtproto.Account, sessionPath string) (*database.Account, error) {
	svc, closeFn, err := store.NewService()
	if err != nil {
		return nil, err
	}
	defer closeFn()

	name := lo.Name
	if name == "" {
		name = acc.DisplayName()
	}

	return svc.Record(accountsvc.RecordInput{
		Kind:        database.AccountKindMTProto,
		Name:        name,
		Phone:       acc.Phone,
		Username:    acc.Username,
		SessionPath: sessionPath,
	})
}

// autoSelectSingleAccount 在库里只有一个账号时自动设为当前账号。
func autoSelectSingleAccount() (bool, error) {
	svc, closeFn, err := store.NewService()
	if err != nil {
		return false, err
	}
	defer closeFn()
	return svc.AutoSelectSingle()
}

// dataDirHint 返回数据目录的可读提示，用于登录成功后的输出。
func dataDirHint() string {
	dir, err := appdir.DataDir()
	if err != nil {
		return ""
	}
	return dir
}
