package serve

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	accountsvc "github.com/ma6254/tg_down/internal/account"
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

	acct, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return server.AccountDeleteResult{}, fmt.Errorf("%w: 账号 ID %d 不存在", server.ErrAccountNotFound, id)
		}
		return server.AccountDeleteResult{}, fmt.Errorf("查询账号 %d: %w", id, err)
	}

	removedSession, err := accountsvc.New(db).Delete(*acct)
	if err != nil {
		return server.AccountDeleteResult{}, fmt.Errorf("删除账号 %d: %w", acct.ID, err)
	}
	removeAvatarCache(*acct)
	return server.AccountDeleteResult{OK: true, RemovedSession: removedSession}, nil
}
