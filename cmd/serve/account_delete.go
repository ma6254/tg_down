package serve

import (
	"context"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/avatar"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// deleteAccount 只删除本地账号记录与 session 文件，不向 Telegram 注销。
//
// 从 Telegram Desktop 导入的账号与桌面端共用 auth key，删除本地副本时
// 不应该影响桌面端的登录状态，所以单独提供这个入口。
func deleteAccount(ctx context.Context, id int64) (server.AccountDeleteResult, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountDeleteResult{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountDeleteResult{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	removed, removedSession, err := accountsvc.New(db).Remove(id)
	if err != nil {
		return server.AccountDeleteResult{}, mapAccountError(err)
	}
	avatar.Remove(removed)
	return server.AccountDeleteResult{OK: true, RemovedSession: removedSession}, nil
}
