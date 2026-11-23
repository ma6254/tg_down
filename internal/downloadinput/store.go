package downloadinput

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ma6254/tg_down/internal/database"
)

// Store 在 database 之上实现草稿与历史的业务规则。
type Store struct {
	db *database.DB
}

// NewStore 绑定一个数据库句柄。
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// GetDraft 返回下载页草稿；没有草稿（或内容已损坏）时返回默认值。
func (s *Store) GetDraft() (Draft, error) {
	row, err := s.db.GetUIDraft(DraftKey)
	if errors.Is(err, database.ErrUIDraftNotFound) {
		return Draft{}, nil
	}
	if err != nil {
		return Draft{}, err
	}

	var stored storedDraft
	if err := json.Unmarshal([]byte(row.Value), &stored); err != nil {
		// 损坏的草稿不应让下载页打不开：当作空草稿，下次保存会覆盖。
		return Draft{UpdatedAt: row.UpdatedAt}, nil
	}

	return Draft{
		Links:     stored.Links,
		Cache:     decodeCache(stored.Cache),
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// SaveDraft 全量保存草稿并返回落库后的结果。
//
// links 超限时返回 ErrInvalid；cache 省略时保留旧缓存，null 或非法时清空，
// 非法缓存不会影响输入本身的保存。
func (s *Store) SaveDraft(req SaveDraftRequest) (Draft, error) {
	if err := ValidateLinks(req.Links); err != nil {
		return Draft{}, err
	}

	current, err := s.GetDraft()
	if err != nil {
		return Draft{}, err
	}

	var cache *Cache
	switch {
	case req.Cache == nil:
		// 自动保存只带输入时保留上次摘要，避免把刚解析的卡片冲掉。
		cache = current.Cache
	case isJSONNull(req.Cache):
		cache = nil
	default:
		parsed, parseErr := ParseCache(req.Cache)
		if parseErr != nil {
			cache = nil
		} else {
			cache = parsed
		}
	}

	value, err := json.Marshal(storedDraft{
		Links: req.Links,
		Cache: encodeCache(cache),
	})
	if err != nil {
		return Draft{}, fmt.Errorf("序列化草稿: %w", err)
	}

	row, err := s.db.SaveUIDraft(DraftKey, string(value))
	if err != nil {
		return Draft{}, err
	}
	return Draft{
		Links:     req.Links,
		Cache:     cache,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// Record 记录一次成功解析的输入并返回落库后的记录。
//
// 相同 links（忽略两个全局开关）重复记录时更新使用次数、开关快照与摘要，
// 不新增行；随后裁剪到最近 HistoryLimit 条。created 表示这次是否新建。
func (s *Store) Record(req RecordRequest) (entry Entry, created bool, err error) {
	normalized, lineCount, firstLine, err := inspectLinks(req.Links)
	if err != nil {
		return Entry{}, false, err
	}

	var cache *Cache
	cacheJSON := ""
	if len(req.Cache) > 0 {
		if parsed, parseErr := ParseCache(req.Cache); parseErr == nil && parsed != nil {
			cache = parsed
			encoded, marshalErr := json.Marshal(parsed)
			if marshalErr != nil {
				return Entry{}, false, fmt.Errorf("序列化解析摘要: %w", marshalErr)
			}
			cacheJSON = string(encoded)
		}
	}

	row := database.DownloadInput{
		Hash:            Hash(normalized),
		Links:           req.Links,
		LineCount:       lineCount,
		FirstLine:       firstLine,
		CacheJSON:       cacheJSON,
		ExpandTextLinks: req.ExpandTextLinks,
		Overwrite:       req.Overwrite,
	}
	created, err = s.db.SaveDownloadInput(&row)
	if err != nil {
		return Entry{}, false, err
	}
	if err := s.db.PruneDownloadInputs(HistoryLimit); err != nil {
		return Entry{}, created, err
	}

	// row 已被回填成落库后的完整记录，但 Cache 用本次解析的可读结构。
	entry = entryFromRow(row)
	entry.Cache = cache
	entry.CacheTruncated = cacheTruncated(cache)
	entry.Title = SummaryTitle(cache)
	return entry, created, nil
}

// List 返回最近使用的历史记录；limit 会被收敛到 1..HistoryLimit。
func (s *Store) List(limit int) ([]Entry, error) {
	if limit <= 0 || limit > HistoryLimit {
		limit = HistoryLimit
	}
	rows, err := s.db.ListDownloadInputs(limit)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, entryFromRow(row))
	}
	return entries, nil
}

// Get 返回单条历史记录；不存在时返回 database.ErrDownloadInputNotFound。
func (s *Store) Get(id uint) (Entry, error) {
	row, err := s.db.GetDownloadInput(id)
	if err != nil {
		return Entry{}, err
	}
	return entryFromRow(row), nil
}

// Delete 删除单条历史记录。
func (s *Store) Delete(id uint) error {
	return s.db.DeleteDownloadInput(id)
}

// Clear 清空历史记录，返回删除条数。
func (s *Store) Clear() (int, error) {
	removed, err := s.db.DeleteAllDownloadInputs()
	if err != nil {
		return 0, err
	}
	return int(removed), nil
}

// entryFromRow 把数据库记录转换成领域模型并解析缓存。
func entryFromRow(row database.DownloadInput) Entry {
	cache := decodeCache(json.RawMessage(row.CacheJSON))
	return Entry{
		ID:              row.ID,
		Links:           row.Links,
		LineCount:       row.LineCount,
		FirstLine:       row.FirstLine,
		Title:           SummaryTitle(cache),
		ExpandTextLinks: row.ExpandTextLinks,
		Overwrite:       row.Overwrite,
		UseCount:        row.UseCount,
		Cache:           cache,
		CacheTruncated:  cacheTruncated(cache),
		CreatedAt:       row.CreatedAt,
		LastUsedAt:      row.LastUsedAt,
	}
}

// decodeCache 解析落库的缓存；缺失或损坏时返回 nil（回填不受影响）。
func decodeCache(raw json.RawMessage) *Cache {
	if len(raw) == 0 {
		return nil
	}
	cache, err := ParseCache(raw)
	if err != nil {
		return nil
	}
	return cache
}

// encodeCache 把缓存编码为可嵌入草稿的 JSON；nil 返回 nil（序列化时省略）。
func encodeCache(cache *Cache) json.RawMessage {
	if cache == nil {
		return nil
	}
	encoded, err := json.Marshal(cache)
	if err != nil {
		return nil
	}
	return encoded
}

// cacheTruncated 表示预览总数多于缓存的消息条目（只缓存前 20 条）。
func cacheTruncated(cache *Cache) bool {
	return cache != nil && cache.Summary.Total > len(cache.Items)
}

// LegacySwitches 是旧版本草稿里保存的两个开关；nil 表示旧草稿里没有该字段。
type LegacySwitches struct {
	ExpandTextLinks *bool
	Overwrite       *bool
}

// LegacyDraftSwitches 读取旧版本草稿里保存的开关，用于迁移到 settings。
//
// 这两个字段已从草稿结构移除；升级时把用户原来的值写进
// settings.download_expand_text_links / download_overwrite。
func (s *Store) LegacyDraftSwitches() (LegacySwitches, error) {
	row, err := s.db.GetUIDraft(DraftKey)
	if errors.Is(err, database.ErrUIDraftNotFound) {
		return LegacySwitches{}, nil
	}
	if err != nil {
		return LegacySwitches{}, err
	}
	var legacy struct {
		ExpandTextLinks *bool `json:"expand_text_links"`
		Overwrite       *bool `json:"overwrite"`
	}
	if err := json.Unmarshal([]byte(row.Value), &legacy); err != nil {
		return LegacySwitches{}, nil
	}
	return LegacySwitches{
		ExpandTextLinks: legacy.ExpandTextLinks,
		Overwrite:       legacy.Overwrite,
	}, nil
}

// NormalizeHashes 按当前去重规则重算历史 hash，并合并旧版本留下的重复行。
//
// 旧版本的 hash 包含两个开关，同一批链接切换开关预览会留下多行；
// 启动时调用一次，把同 links 的记录收敛成一条（保留最近使用的那行，
// 次数相加，开关快照与摘要取最近一次）。
func (s *Store) NormalizeHashes() error {
	rows, err := s.db.ListDownloadInputs(1000)
	if err != nil {
		return err
	}

	type group struct {
		row      database.DownloadInput
		useCount int
	}
	groups := make(map[string]*group, len(rows))
	order := make([]string, 0, len(rows))
	duplicateIDs := make([]uint, 0, len(rows))

	for _, row := range rows {
		normalized, _, _, err := inspectLinks(row.Links)
		if err != nil {
			// 旧数据超限时跳过：保留原样，不因为规范化让记录消失。
			continue
		}
		hash := Hash(normalized)
		if existing, ok := groups[hash]; ok {
			// 列表按最近使用倒序，先遇到的是更新的那行。
			existing.useCount += row.UseCount
			duplicateIDs = append(duplicateIDs, row.ID)
			continue
		}
		groups[hash] = &group{row: row, useCount: row.UseCount}
		order = append(order, hash)
	}

	// 先删重复行，再更新保留行的 hash，避免唯一索引在中间状态冲突。
	for _, id := range duplicateIDs {
		if err := s.db.DeleteDownloadInput(id); err != nil {
			return err
		}
	}
	for _, hash := range order {
		g := groups[hash]
		fields := map[string]any{"use_count": g.useCount}
		if g.row.Hash != hash {
			fields["hash"] = hash
		}
		if err := s.db.UpdateDownloadInput(g.row.ID, fields); err != nil {
			return err
		}
	}
	return nil
}

// CoverThumbKey 返回摘要里第一条带缩略图的消息的磁盘缓存键；没有时返回空串。
//
// 历史卡片用它在 thumbnail/ 里取封面，与预览 token 是否过期无关。
func (e Entry) CoverThumbKey() string {
	if e.Cache == nil {
		return ""
	}
	for _, item := range e.Cache.Items {
		if item.ThumbKey != "" {
			return item.ThumbKey
		}
	}
	return ""
}
