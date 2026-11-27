package serve

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/download/manager"
	"github.com/ma6254/tg_down/internal/downloadinput"
	"github.com/ma6254/tg_down/internal/msgcache"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/thumbcache"
	"github.com/ma6254/tg_down/internal/utils"
)

// openDownloadInputStore 打开数据库并返回草稿/历史仓库与关闭函数。
//
// HTTP 请求之间不共享连接：与账号、配置等接口保持一致，单用户场景下
// SQLite 打开成本可以忽略，换来的是不需要管理常驻句柄的生命周期。
func openDownloadInputStore() (*downloadinput.Store, func(), error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, func() {}, fmt.Errorf("打开数据库: %w", err)
	}
	return downloadinput.NewStore(db), func() { _ = db.Close() }, nil
}

// mapDraftError 把草稿领域错误翻译成 server 的哨兵错误。
func mapDraftError(err error) error {
	if errors.Is(err, downloadinput.ErrInvalid) {
		return fmt.Errorf("%w: %v", server.ErrDownloadDraftInvalid, err)
	}
	return err
}

// mapHistoryError 把历史领域错误翻译成 server 的哨兵错误。
func mapHistoryError(err error) error {
	switch {
	case errors.Is(err, downloadinput.ErrInvalid):
		return fmt.Errorf("%w: %v", server.ErrDownloadHistoryInvalid, err)
	case errors.Is(err, database.ErrDownloadInputNotFound):
		return fmt.Errorf("%w: %v", server.ErrDownloadHistoryNotFound, err)
	}
	return err
}

// messageCacheRetention 是消息缓存的保留期：超过这么久没被用过的行会被清掉。
const messageCacheRetention = 90 * 24 * time.Hour

// pruneMessageCache 删除长期未使用的消息缓存。
//
// 分小批删除，避免一次性拿写锁太久；服务启动阶段调用，失败只记录日志。
func pruneMessageCache() error {
	db, err := utils.OpenDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	cache := msgcache.New(db)
	before := time.Now().Add(-messageCacheRetention)

	var total int64
	for i := 0; i < 20; i++ {
		removed, err := cache.Prune(before, 500)
		if err != nil {
			return err
		}
		total += removed
		if removed < 500 {
			break
		}
	}
	if total > 0 {
		log.Printf("清理消息缓存 %d 行", total)
	}
	// 会话定位缓存按自身有效期清理，与消息缓存的保留期无关。
	peers, err := cache.PrunePeers(500)
	if err != nil {
		return err
	}
	if peers > 0 {
		log.Printf("清理会话缓存 %d 行", peers)
	}
	threads, err := cache.PruneThreads(500)
	if err != nil {
		return err
	}
	if threads > 0 {
		log.Printf("清理评论线程缓存 %d 行", threads)
	}
	return nil
}

// normalizeDownloadHistory 修正旧版本留下的重复历史记录。
//
// 旧版本把「覆盖同名文件」开关计入 hash，同一批链接切换开关会多出一行；
// 启动时按当前规则重算并合并。失败只记录日志，不阻止服务启动。
func normalizeDownloadHistory() error {
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return err
	}
	defer closeStore()
	return store.NormalizeHashes()
}

// ensureDownloadSettings 确保 settings 里有下载页与网页相关的全局设置。
//
// SeedDefaults 只处理空表，老库不会自动补新键；这里补一次：
// 旧草稿里保存过开关值时沿用，否则写默认值。已有配置（用户后来改过）不动。
// workers 是启动参数 --max-concurrency，仅在该键还不存在时作为初始值。
func ensureDownloadSettings(workers int) error {
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return err
	}
	legacy, err := store.LegacyDraftSwitches()
	closeStore()
	if err != nil {
		return err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	settings := config.NewStore(db)
	ensure := func(key, defaultValue string, legacyValue *bool) error {
		if _, ok, err := settings.Get(key); err != nil || ok {
			return err
		}
		value := defaultValue
		if legacyValue != nil {
			value = strconv.FormatBool(*legacyValue)
		}
		return settings.Set(key, value)
	}
	if err := ensure(config.KeyDownloadOverwrite, "false", legacy.Overwrite); err != nil {
		return err
	}
	if err := ensure(config.KeyDownloadExpandTextLinks, "false", legacy.ExpandTextLinks); err != nil {
		return err
	}
	// 自动解析没有旧草稿字段可继承，保持默认开启（与原行为一致）。
	if err := ensure(config.KeyDownloadAutoPreview, "true", nil); err != nil {
		return err
	}
	if err := ensure(config.KeyDownloadWorkers, strconv.Itoa(workers), nil); err != nil {
		return err
	}
	// 网页主题与标题速度都是新增的前端偏好；老库也补一行，保证 settings 表中可见。
	if err := ensure(config.KeyWebTheme, config.DefaultWebTheme, nil); err != nil {
		return err
	}
	if err := ensure(config.KeyWebTitleSpeed, config.DefaultWebTitleSpeed, nil); err != nil {
		return err
	}
	return ensure(config.KeyDownloadThreads, strconv.Itoa(manager.DefaultThreads), nil)
}

// getDownloadDraft 是 Config.GetDownloadDraft 的实现。
func getDownloadDraft(ctx context.Context) (server.DownloadDraft, error) {
	if err := ctx.Err(); err != nil {
		return server.DownloadDraft{}, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.DownloadDraft{}, err
	}
	defer closeStore()

	draft, err := store.GetDraft()
	if err != nil {
		return server.DownloadDraft{}, fmt.Errorf("读取下载草稿: %w", err)
	}
	return server.NewDownloadDraft(draft), nil
}

// saveDownloadDraft 是 Config.SaveDownloadDraft 的实现。
func saveDownloadDraft(ctx context.Context, input server.DownloadDraftInput) (server.DownloadDraft, error) {
	if err := ctx.Err(); err != nil {
		return server.DownloadDraft{}, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.DownloadDraft{}, err
	}
	defer closeStore()

	draft, err := store.SaveDraft(downloadinput.SaveDraftRequest{
		Links: input.Links,
		Cache: input.Cache,
	})
	if err != nil {
		return server.DownloadDraft{}, mapDraftError(err)
	}
	return server.NewDownloadDraft(draft), nil
}

// listDownloadHistory 是 Config.ListDownloadHistory 的实现。
func listDownloadHistory(ctx context.Context, limit int) (server.DownloadHistoryList, error) {
	if err := ctx.Err(); err != nil {
		return server.DownloadHistoryList{}, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.DownloadHistoryList{}, err
	}
	defer closeStore()

	entries, err := store.List(limit)
	if err != nil {
		return server.DownloadHistoryList{}, fmt.Errorf("读取下载历史: %w", err)
	}
	return server.NewDownloadHistoryList(entries), nil
}

// getDownloadHistory 是 Config.GetDownloadHistory 的实现。
func getDownloadHistory(ctx context.Context, id uint) (server.DownloadHistoryDetail, error) {
	if err := ctx.Err(); err != nil {
		return server.DownloadHistoryDetail{}, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.DownloadHistoryDetail{}, err
	}
	defer closeStore()

	entry, err := store.Get(id)
	if err != nil {
		return server.DownloadHistoryDetail{}, mapHistoryError(err)
	}
	return server.NewDownloadHistoryDetail(entry), nil
}

// recordDownloadHistory 是 Config.RecordDownloadHistory 的实现。
func recordDownloadHistory(ctx context.Context, input server.DownloadHistoryInput) (server.DownloadHistoryDetail, bool, error) {
	if err := ctx.Err(); err != nil {
		return server.DownloadHistoryDetail{}, false, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.DownloadHistoryDetail{}, false, err
	}
	defer closeStore()

	entry, created, err := store.Record(downloadinput.RecordRequest{
		Links:           input.Links,
		ExpandTextLinks: input.ExpandTextLinks,
		Overwrite:       input.Overwrite,
		Cache:           input.Cache,
	})
	if err != nil {
		return server.DownloadHistoryDetail{}, false, mapHistoryError(err)
	}
	return server.NewDownloadHistoryDetail(entry), created, nil
}

// deleteDownloadHistory 是 Config.DeleteDownloadHistory 的实现。
func deleteDownloadHistory(ctx context.Context, id uint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return err
	}
	defer closeStore()

	return mapHistoryError(store.Delete(id))
}

// clearDownloadHistory 是 Config.ClearDownloadHistory 的实现。
func clearDownloadHistory(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return 0, err
	}
	defer closeStore()

	removed, err := store.Clear()
	if err != nil {
		return 0, fmt.Errorf("清空下载历史: %w", err)
	}
	return removed, nil
}

// getDownloadHistoryCover 是 Config.GetDownloadHistoryCover 的实现。
//
// 封面来自 <数据目录>/thumbnail 的磁盘缓存：缓存键在预览时随摘要一起落库，
// 因此 token 过期或进程重启后仍可读取；文件不存在时返回 404，前端退回图标。
func getDownloadHistoryCover(ctx context.Context, id uint) (server.MediaThumbnail, error) {
	if err := ctx.Err(); err != nil {
		return server.MediaThumbnail{}, err
	}
	store, closeStore, err := openDownloadInputStore()
	if err != nil {
		return server.MediaThumbnail{}, err
	}
	defer closeStore()

	entry, err := store.Get(id)
	if err != nil {
		return server.MediaThumbnail{}, mapHistoryError(err)
	}
	key := entry.CoverThumbKey()
	if key == "" {
		return server.MediaThumbnail{}, fmt.Errorf("%w: 这条历史没有封面", server.ErrThumbnailNotFound)
	}
	data, ok := thumbcache.Get(key)
	if !ok {
		return server.MediaThumbnail{}, fmt.Errorf("%w: 封面缓存已不存在", server.ErrThumbnailNotFound)
	}
	return server.NewMediaThumbnail(data), nil
}
