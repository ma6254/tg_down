package serve

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// logoutAccount 退出登录：尽力吊销 Telegram session，然后删除本地账号记录与文件。
//
// 联网失败不阻断本地清理，返回 Revoked=false 让前端提示；已下载的文件不受影响。
func logoutAccount(ctx context.Context, id int64) (server.AccountLogoutResult, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountLogoutResult{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountLogoutResult{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	acct, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return server.AccountLogoutResult{}, fmt.Errorf("%w: 账号 ID %d 不存在", server.ErrAccountNotFound, id)
		}
		return server.AccountLogoutResult{}, fmt.Errorf("查询账号 %d: %w", id, err)
	}

	revoked := false
	if acct.Kind == database.AccountKindMTProto && utils.FileExists(acct.SessionPath) {
		proxy, proxyErr := config.NewStore(db).Proxy()
		if proxyErr == nil {
			app, appErr := accountsvc.New(db).AppOrDefault()
			if appErr == nil {
				session := mtproto.Session{App: app, SessionPath: acct.SessionPath, Proxy: proxy}
				callCtx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
				if err := session.Logout(callCtx); err == nil {
					revoked = true
				}
				cancel()
			}
		}
	}

	removedSession, err := accountsvc.New(db).Delete(*acct)
	if err != nil {
		return server.AccountLogoutResult{}, fmt.Errorf("删除账号 %d: %w", acct.ID, err)
	}
	removeAvatarCache(*acct)
	return server.AccountLogoutResult{OK: true, Revoked: revoked, RemovedSession: removedSession}, nil
}
