package database

import (
	"time"

	"gorm.io/gorm/clause"
)

// DefaultSettingType 是配置值的默认类型。
const DefaultSettingType = "string"

// Setting 是一条系统配置（键值对）。
//
// Type 描述 Value 的逻辑类型（string / integer / boolean 等），
// 供 API 与前端选择输入控件，不影响底层存储（Value 始终是文本）。
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Type      string    `gorm:"size:16;not null;default:string" json:"type"`
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

// SetSetting 写入或更新配置项，类型使用默认的 string。
func (d *DB) SetSetting(key, value string) error {
	return d.SetSettingTyped(key, DefaultSettingType, value)
}

// SetSettingTyped 写入或更新配置项及其逻辑类型。
func (d *DB) SetSettingTyped(key, valueType, value string) error {
	if valueType == "" {
		valueType = DefaultSettingType
	}
	item := Setting{Key: key, Type: valueType, Value: value}
	return d.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"type":  valueType,
			"value": value,
		}),
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
