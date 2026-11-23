package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrUIDraftNotFound 表示界面草稿不存在。
var ErrUIDraftNotFound = errors.New("界面草稿不存在")

// UIDraft 是一条界面输入草稿。
//
// Value 存 JSON 文本而不是拆列：字段少且后续可能增加，
// 结构校验由 internal/downloadinput 负责，表只做键值存储。
type UIDraft struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"type:text;not null"`
	UpdatedAt time.Time
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (UIDraft) TableName() string { return "ui_drafts" }

// GetUIDraft 按 key 读取草稿；不存在时返回 ErrUIDraftNotFound。
func (d *DB) GetUIDraft(key string) (UIDraft, error) {
	var draft UIDraft
	if err := d.First(&draft, "key = ?", key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UIDraft{}, ErrUIDraftNotFound
		}
		return UIDraft{}, err
	}
	return draft, nil
}

// SaveUIDraft 覆盖写入草稿（单行 upsert），返回落库后的记录。
func (d *DB) SaveUIDraft(key, value string) (UIDraft, error) {
	draft := UIDraft{Key: key, Value: value, UpdatedAt: time.Now()}
	if err := d.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&draft).Error; err != nil {
		return UIDraft{}, err
	}

	// OnConflict 的 DoUpdates 不会回填内存值，重新读一次确保 updated_at 一致。
	saved, err := d.GetUIDraft(key)
	if err != nil {
		return UIDraft{}, err
	}
	return saved, nil
}
