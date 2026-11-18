package database

import (
	"time"

	"gorm.io/gorm/clause"
)

// Setting 是一条系统配置（键值对）。
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Setting) TableName() string { return "settings" }

// GetSetting 读取配置项，不存在时返回 gorm.ErrRecordNotFound。
func (d *DB) GetSetting(key string) (string, error) {
	var item Setting
	if err := d.DB.First(&item, "key = ?", key).Error; err != nil {
		return "", err
	}
	return item.Value, nil
}

// SetSetting 写入或更新配置项。
func (d *DB) SetSetting(key, value string) error {
	item := Setting{Key: key, Value: value}
	return d.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": value}),
	}).Create(&item).Error
}

// DeleteSetting 删除配置项，不存在时返回 nil。
func (d *DB) DeleteSetting(key string) error {
	return d.DB.Delete(&Setting{}, "key = ?", key).Error
}

// ListSettings 返回全部配置项，按 key 排序。
func (d *DB) ListSettings() ([]Setting, error) {
	var items []Setting
	if err := d.DB.Order("key ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
