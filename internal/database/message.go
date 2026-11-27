package database

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 消息缓存的状态。
const (
	// MessageStatusOK 表示取到了消息（可能没有媒体，看 Payload 里的 Media 是否为空）。
	MessageStatusOK = "ok"
	// MessageStatusGone 表示消息已删除或当前账号不可见；Payload 保留上次快照。
	MessageStatusGone = "gone"
)

// MessageCache 是一条消息的预览快照，唯一键 (chat_id, message_id)。
//
// Payload 是白名单 JSON（见 internal/msgcache），只整取整写、不做字段级查询，
// 所以没有拆成普通列；需要按字段检索时再加生成列并建索引。
// StatusAccount 只对 gone 有意义：记录是谁确认的不可见，避免换个账号
// 把另一个账号的 ok 快照覆盖掉。
type MessageCache struct {
	ID            uint   `gorm:"primaryKey"`
	ChatID        int64  `gorm:"not null;uniqueIndex:idx_message_key"`
	MessageID     int    `gorm:"not null;uniqueIndex:idx_message_key"`
	Status        string `gorm:"size:8;not null"`
	StatusAccount int64
	Payload       string `gorm:"type:text"`
	SchemaVer     int
	FetchedAt     time.Time
	ExpiresAt     time.Time `gorm:"index"`
	LastUsedAt    time.Time `gorm:"index"`
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (MessageCache) TableName() string { return "message_cache" }

// MessageStore 读写 message_cache 表。
type MessageStore struct {
	db *DB
}

// NewMessageStore 创建消息缓存仓库。
func NewMessageStore(db *DB) *MessageStore {
	return &MessageStore{db: db}
}

// Lookup 按会话批量读取缓存行，返回以消息 ID 索引的 map。
//
// 不判断过期与状态：TTL 与账号可见性属于缓存策略，放在 internal/msgcache 里。
func (s *MessageStore) Lookup(chatID int64, ids []int) (map[int]MessageCache, error) {
	out := make(map[int]MessageCache, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	var rows []MessageCache
	if err := s.db.
		Where("chat_id = ? AND message_id IN ?", chatID, ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.MessageID] = row
	}
	return out, nil
}

// Save 批量写入快照；同一 (chat_id, message_id) 已存在时覆盖可刷新字段。
//
// last_used_at 不在覆盖列表里，由 Touch 单独维护，否则每次刷新都会打乱 LRU 顺序。
func (s *MessageStore) Save(rows []MessageCache) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chat_id"}, {Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "status_account", "payload", "schema_ver",
			"fetched_at", "expires_at",
		}),
	}).CreateInBatches(rows, 100).Error
}

// MarkGone 记录「消息不存在」的结论，只更新状态与过期时间，保留已有 payload。
//
// 保留快照是刻意的：消息被删后历史卡片仍应展示上次取到的正文，
// 只是不能再点下载。行不存在时插入一条空 payload 的记录作为负缓存。
//
// fetched_at 不在覆盖列表里：它表示「这份 payload 取得的时间」，
// 标记失效不该把它改成当前时间，否则界面会把旧正文显示成刚缓存的。
func (s *MessageStore) MarkGone(rows []MessageCache) error {
	if len(rows) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "chat_id"}, {Name: "message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "status_account", "expires_at",
		}),
	}).CreateInBatches(rows, 100).Error
}

// Touch 更新命中行的最近使用时间，供清理时按 LRU 判断。
func (s *MessageStore) Touch(chatID int64, ids []int, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.Model(&MessageCache{}).
		Where("chat_id = ? AND message_id IN ?", chatID, ids).
		Update("last_used_at", at).Error
}

// Prune 删除最近使用时间早于 before 的缓存行，单次最多 limit 行。
//
// 用子查询而不是 DELETE ... LIMIT：后者要求 SQLite 编译时开启
// SQLITE_ENABLE_UPDATE_DELETE_LIMIT，modernc 驱动不保证有。
func (s *MessageStore) Prune(before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 500
	}
	sub := s.db.Model(&MessageCache{}).
		Select("id").
		Where("last_used_at < ?", before).
		Limit(limit)
	res := s.db.Where("id IN (?)", sub).Delete(&MessageCache{})
	return res.RowsAffected, res.Error
}

// Count 返回消息缓存的行数。
func (s *MessageStore) Count() (int64, error) {
	var n int64
	err := s.db.Model(&MessageCache{}).Count(&n).Error
	return n, err
}

// Clear 删除全部消息快照，返回删除的行数。
func (s *MessageStore) Clear() (int64, error) {
	res := s.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&MessageCache{})
	return res.RowsAffected, res.Error
}
