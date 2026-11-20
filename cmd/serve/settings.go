package serve

import (
	"context"
	"fmt"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/utils"
)

// getSettings 返回数据库里的全部配置项，等价于 CLI 的 config list。
//
// 只读标记、说明与脱敏值由 internal/server 统一映射，这里只负责读库。
func getSettings(ctx context.Context) ([]config.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	entries, err := config.NewStore(db).List()
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}
	return entries, nil
}

// updateSettings 部分更新配置，返回更新后的全量配置。
//
// 值为 nil 表示删除（config unset），未出现的键保持原值；
// 校验、脱敏回填与写入都在 internal/config，这里只负责打开数据库。
func updateSettings(ctx context.Context, patch map[string]*string) ([]config.Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	return config.NewStore(db).Apply(patch)
}
