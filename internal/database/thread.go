package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ThreadCache 是「帖子 → 讨论组线程」的定位缓存。
//
// Payload 是白名单 JSON（评论区的会话与线程起点），只按 (账号, 键) 整取整写；
// 读评论页本身不缓存，评论会不断新增，缓存它容易显示旧内容。
type ThreadCache struct {
	ID        uint   `gorm:"primaryKey"`
	AccountID int64  `gorm:"not null;uniqueIndex:idx_thread_cache_key"`
	LookupKey string `gorm:"size:96;not null;uniqueIndex:idx_thread_cache_key"`

	Payload string `gorm:"type:text"`

	FetchedAt time.Time
	ExpiresAt time.Time `gorm:"index"`
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (ThreadCache) TableName() string { return "thread_cache" }

// ThreadStore 读写 thread_cache 表。
type ThreadStore struct {
	db *DB
}

// NewThreadStore 创建评论线程缓存仓库。
func NewThreadStore(db *DB) *ThreadStore {
	return &ThreadStore{db: db}
}

// LoadThreads 按 (账号, 键) 批量读取。
func (s *ThreadStore) LoadThreads(accountID int64, keys []string) (map[string]ThreadCache, error) {
	out := make(map[string]ThreadCache, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	var rows []ThreadCache
	if err := s.db.
		Where("account_id = ? AND lookup_key IN ?", accountID, keys).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.LookupKey] = row
	}
	return out, nil
}

// SaveThreads 批量写入或覆盖评论线程定位结果。
func (s *ThreadStore) SaveThreads(rows []ThreadCache) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "lookup_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"payload", "fetched_at", "expires_at",
		}),
	}).CreateInBatches(rows, 100).Error
}

// PruneExpiredThreads 删除过期时间早于 before 的行，单次最多 limit 行。
func (s *ThreadStore) PruneExpiredThreads(before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	sub := s.db.Model(&ThreadCache{}).
		Select("id").
		Where("expires_at < ?", before).
		Limit(limit)
	res := s.db.Where("id IN (?)", sub).Delete(&ThreadCache{})
	return res.RowsAffected, res.Error
}

// Count 返回评论线程缓存的行数。
func (s *ThreadStore) Count() (int64, error) {
	var n int64
	err := s.db.Model(&ThreadCache{}).Count(&n).Error
	return n, err
}

// Clear 删除全部评论线程定位结果，返回删除的行数。
func (s *ThreadStore) Clear() (int64, error) {
	res := s.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&ThreadCache{})
	return res.RowsAffected, res.Error
}
