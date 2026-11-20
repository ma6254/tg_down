package serve

import (
	"context"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/avatar"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// logoutAccount 退出登录：尽力吊销 Telegram session，然后删除本地账号记录与文件。
//
// 桌面导入的账号只能删除（见 deleteAccount），这里会返回冲突错误。
func logoutAccount(ctx context.Context, id int64) (server.AccountLogoutResult, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountLogoutResult{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountLogoutResult{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	result, err := accountsvc.New(db).Logout(ctx, id)
	if err != nil {
		return server.AccountLogoutResult{}, mapAccountError(err)
	}
	avatar.Remove(result.Removed)
	return server.AccountLogoutResult{OK: true, Revoked: result.Revoked, RemovedSession: result.RemovedFile}, nil
}
