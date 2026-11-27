package serve

import (
	"context"

	"github.com/ma6254/tg_down/internal/msgcache"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/utils"
)

// cacheStats 返回本地缓存规模，供设置页展示。
func cacheStats(ctx context.Context) (server.CacheStats, error) {
	if err := ctx.Err(); err != nil {
		return server.CacheStats{}, err
	}
	db, err := utils.OpenDatabase()
	if err != nil {
		return server.CacheStats{}, err
	}
	defer db.Close()

	stats, err := msgcache.New(db).Stats()
	if err != nil {
		return server.CacheStats{}, err
	}
	return toCacheStats(stats), nil
}

// clearCache 清空本地缓存并返回删除的行数。
func clearCache(ctx context.Context) (server.CacheStats, error) {
	if err := ctx.Err(); err != nil {
		return server.CacheStats{}, err
	}
	db, err := utils.OpenDatabase()
	if err != nil {
		return server.CacheStats{}, err
	}
	defer db.Close()

	removed, err := msgcache.New(db).Clear()
	if err != nil {
		return server.CacheStats{}, err
	}
	return toCacheStats(removed), nil
}

// toCacheStats 把领域计数转成接口结构。
func toCacheStats(stats msgcache.Stats) server.CacheStats {
	return server.CacheStats{
		Messages: stats.Messages,
		Peers:    stats.Peers,
		Threads:  stats.Threads,
	}
}
