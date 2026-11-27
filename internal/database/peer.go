package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PeerCache 是会话定位缓存：链接 → 会话。
//
// 预览时最贵的一步不是读消息，而是「找到消息属于哪个会话」：
// 用户名要走 contacts.resolveUsername，私有频道链接还要拉会话列表。
// 这张表记住上一轮的结果，命中后整轮预览可以不连 Telegram。
//
// 键里包含账号：access_hash 是按账号签发的，换账号必须重新解析。
type PeerCache struct {
	ID        uint   `gorm:"primaryKey"`
	AccountID int64  `gorm:"not null;uniqueIndex:idx_peer_cache_key"`
	LookupKey string `gorm:"size:160;not null;uniqueIndex:idx_peer_cache_key"`

	Kind       string `gorm:"size:8;not null"`
	PeerID     int64  `gorm:"not null"`
	AccessHash int64
	Title      string `gorm:"size:255"`
	Username   string `gorm:"size:64"`
	Broadcast  bool
	Self       bool

	// MaxSize 是写入时该账号的单文件上限，顺带缓存，
	// 这样全部命中时不必为了读 Premium 状态再连一次。
	MaxSize int64

	FetchedAt time.Time
	ExpiresAt time.Time `gorm:"index"`
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (PeerCache) TableName() string { return "peer_cache" }

// PeerStore 读写 peer_cache 表。
type PeerStore struct {
	db *DB
}

// NewPeerStore 创建会话缓存仓库。
func NewPeerStore(db *DB) *PeerStore {
	return &PeerStore{db: db}
}

// LoadPeers 按 (账号, 键) 批量读取，返回以键索引的 map。
func (s *PeerStore) LoadPeers(accountID int64, keys []string) (map[string]PeerCache, error) {
	out := make(map[string]PeerCache, len(keys))
	if len(keys) == 0 {
		return out, nil
	}

	var rows []PeerCache
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

// SavePeers 批量写入或覆盖会话定位结果。
func (s *PeerStore) SavePeers(rows []PeerCache) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_id"}, {Name: "lookup_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"kind", "peer_id", "access_hash", "title", "username",
			"broadcast", "self", "max_size", "fetched_at", "expires_at",
		}),
	}).CreateInBatches(rows, 100).Error
}

// DeletePeers 删除失效的会话定位结果。
func (s *PeerStore) DeletePeers(accountID int64, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	res := s.db.
		Where("account_id = ? AND lookup_key IN ?", accountID, keys).
		Delete(&PeerCache{})
	return res.RowsAffected, res.Error
}

// PruneExpired 删除过期时间早于 before 的会话定位结果，单次最多 limit 行。
func (s *PeerStore) PruneExpired(before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	sub := s.db.Model(&PeerCache{}).
		Select("id").
		Where("expires_at < ?", before).
		Limit(limit)
	res := s.db.Where("id IN (?)", sub).Delete(&PeerCache{})
	return res.RowsAffected, res.Error
}

// Count 返回会话缓存的行数。
func (s *PeerStore) Count() (int64, error) {
	var n int64
	err := s.db.Model(&PeerCache{}).Count(&n).Error
	return n, err
}

// Clear 删除全部会话定位结果，返回删除的行数。
func (s *PeerStore) Clear() (int64, error) {
	res := s.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&PeerCache{})
	return res.RowsAffected, res.Error
}
