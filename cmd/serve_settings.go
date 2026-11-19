package cmd

import (
	"context"
	"fmt"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// readOnlySettings 是不能通过 HTTP 直接写的配置项，由专门的接口维护。
var readOnlySettings = map[string]bool{
	config.KeyCurrentAccountID: true,
}

// getSettings 返回数据库里的全部配置项，等价于 CLI 的 config list。
func getSettings(ctx context.Context) ([]server.Setting, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	return listSettings(db)
}

// updateSettings 部分更新配置，返回更新后的全量配置。
//
// 值为 nil 表示删除（config unset），未出现的键保持原值。
// 先整体校验再写入，避免写一半失败留下半套配置。
func updateSettings(ctx context.Context, patch server.SettingsPatch) ([]server.Setting, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	store := config.NewStore(db)

	// 先整体校验再写入，避免写一半失败留下半套配置。
	// 代理密码读取时被脱敏为 ***；编辑时如果原样提交回来，用现有密码回填，
	// 否则会把 *** 当成新密码写进库。
	for key, value := range patch {
		item, ok := config.Lookup(key)
		if !ok {
			return nil, fmt.Errorf("%w: 未知配置项 %q", server.ErrSettingInvalid, key)
		}
		if readOnlySettings[key] {
			return nil, fmt.Errorf("%w: %s 由账号接口维护，请用 POST /api/v1/accounts/{id}/select", server.ErrSettingInvalid, key)
		}
		if value == nil {
			continue
		}
		if key == config.KeyHTTPProxy {
			current, _, err := store.Get(config.KeyHTTPProxy)
			if err != nil {
				return nil, fmt.Errorf("读取现有代理: %w", err)
			}
			merged, err := config.MergeMaskedProxy(current, *value)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", server.ErrSettingInvalid, err)
			}
			value = &merged
			patch[key] = value
		}
		if item.Validate == nil {
			continue
		}
		if err := item.Validate(*value); err != nil {
			return nil, fmt.Errorf("%w: %v", server.ErrSettingInvalid, err)
		}
	}

	for key, value := range patch {
		var err error
		if value == nil {
			err = store.Unset(key)
		} else {
			err = store.Set(key, *value)
		}
		if err != nil {
			return nil, err
		}
	}

	return listSettings(db)
}

// listSettings 把配置记录转换成 API 结构，并补上描述与只读标记。
func listSettings(db *database.DB) ([]server.Setting, error) {
	entries, err := config.NewStore(db).List()
	if err != nil {
		return nil, fmt.Errorf("读取配置: %w", err)
	}

	out := make([]server.Setting, 0, len(entries))
	for _, entry := range entries {
		setting := server.Setting{
			Key: entry.Key,
			// 类型/默认值由代码里的 config.items 定义驱动；旧库启动时也会被 SyncTypes 修正。
			Type:    entry.Type,
			Default: entry.Default,
			// 代理密码不回传明文；前端编辑时按 *** 语义回填。
			Value:    config.Mask(entry.Key, entry.Value),
			Known:    entry.Known,
			ReadOnly: readOnlySettings[entry.Key],
		}
		if item, ok := config.Lookup(entry.Key); ok {
			setting.Description = item.Description
		}
		out = append(out, setting)
	}
	return out, nil
}
