// Package store 为账号子命令提供数据库服务的构造与关闭。
package store

import (
	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/utils"
)

// NewService 打开运行数据目录下的数据库并构造账号服务。
//
// 返回的 close 函数必须调用，用于关闭数据库连接。
func NewService() (*accountsvc.Service, func(), error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, nil, err
	}
	return accountsvc.New(db), func() { _ = db.Close() }, nil
}
