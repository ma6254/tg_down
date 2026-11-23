package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// ErrDownloadInputNotFound 表示下载历史记录不存在。
var ErrDownloadInputNotFound = errors.New("下载历史记录不存在")

// DownloadInput 是一条下载页历史输入（最近解析过的链接批次）。
//
// Hash 是「归一后的 links + 展开开关」的 SHA-256：同内容只保留一行，
// 重复解析时只更新最后使用时间、次数、开关值与摘要缓存。
type DownloadInput struct {
	ID    uint   `gorm:"primaryKey"`
	Hash  string `gorm:"size:64;not null;uniqueIndex"`
	Links string `gorm:"type:text;not null"`

	LineCount int
	FirstLine string `gorm:"size:160"`
	CacheJSON string `gorm:"type:text"`

	ExpandTextLinks bool
	Overwrite       bool

	UseCount  int
	CreatedAt time.Time
	// LastUsedAt 是最近一次解析时间，列表按它倒序。
	LastUsedAt time.Time `gorm:"index"`
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (DownloadInput) TableName() string { return "download_inputs" }

// SaveDownloadInput 按 Hash 写入历史：不存在时新建，存在时更新内容与使用次数。
//
// 返回 created 表示这次是否新建了记录；入参会被回填成落库后的完整记录。
func (d *DB) SaveDownloadInput(input *DownloadInput) (created bool, err error) {
	var existing DownloadInput
	err = d.Where("hash = ?", input.Hash).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now()
		if input.UseCount <= 0 {
			input.UseCount = 1
		}
		if input.CreatedAt.IsZero() {
			input.CreatedAt = now
		}
		if input.LastUsedAt.IsZero() {
			input.LastUsedAt = now
		}
		if err := d.Create(input).Error; err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}

	usedAt := input.LastUsedAt
	if usedAt.IsZero() {
		usedAt = time.Now()
	}
	updates := map[string]any{
		"links":             input.Links,
		"line_count":        input.LineCount,
		"first_line":        input.FirstLine,
		"cache_json":        input.CacheJSON,
		"expand_text_links": input.ExpandTextLinks,
		"overwrite":         input.Overwrite,
		"use_count":         gorm.Expr("use_count + 1"),
		"last_used_at":      usedAt,
	}
	if err := d.Model(&DownloadInput{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
		return false, err
	}

	saved, err := d.GetDownloadInput(existing.ID)
	if err != nil {
		return false, err
	}
	*input = saved
	return false, nil
}

// GetDownloadInput 按 ID 读取历史记录。
func (d *DB) GetDownloadInput(id uint) (DownloadInput, error) {
	var row DownloadInput
	if err := d.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DownloadInput{}, ErrDownloadInputNotFound
		}
		return DownloadInput{}, err
	}
	return row, nil
}

// ListDownloadInputs 按最近使用时间倒序返回历史记录。
func (d *DB) ListDownloadInputs(limit int) ([]DownloadInput, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []DownloadInput
	if err := d.Order("last_used_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateDownloadInput 按 ID 更新指定字段；记录不存在时返回 ErrDownloadInputNotFound。
func (d *DB) UpdateDownloadInput(id uint, fields map[string]any) error {
	result := d.Model(&DownloadInput{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if _, err := d.GetDownloadInput(id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteDownloadInput 删除一条历史记录。
func (d *DB) DeleteDownloadInput(id uint) error {
	result := d.Delete(&DownloadInput{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDownloadInputNotFound
	}
	return nil
}

// DeleteAllDownloadInputs 清空历史记录，返回删除条数。
func (d *DB) DeleteAllDownloadInputs() (int64, error) {
	result := d.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&DownloadInput{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// PruneDownloadInputs 只保留最近 keep 条历史，删除更早的记录。
func (d *DB) PruneDownloadInputs(keep int) error {
	if keep <= 0 {
		return nil
	}
	var ids []uint
	if err := d.Model(&DownloadInput{}).
		Order("last_used_at DESC, id DESC").
		Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) <= keep {
		return nil
	}
	return d.Delete(&DownloadInput{}, ids[keep:]).Error
}
