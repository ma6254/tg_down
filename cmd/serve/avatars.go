package serve

import (
	"context"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// getAccountAvatar 返回账号头像。
//
// 缓存、TTL 与联网刷新在 internal/account（头像文件在 internal/avatar），
// 这里只负责把字节包装成接口结构。
func getAccountAvatar(ctx context.Context, id int64) (server.MediaThumbnail, error) {
	if err := ctx.Err(); err != nil {
		return server.MediaThumbnail{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.MediaThumbnail{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	data, err := accountsvc.New(db).Avatar(ctx, id)
	if err != nil {
		return server.MediaThumbnail{}, mapAccountError(err)
	}
	return server.NewMediaThumbnail(data), nil
}
