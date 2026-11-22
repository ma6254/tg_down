package database

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 下载任务状态。
const (
	DownloadStatusPending = "pending"
	DownloadStatusRunning = "running"
	DownloadStatusPaused  = "paused"
	DownloadStatusDone    = "done"
	DownloadStatusFailed  = "failed"
)

// ErrDownloadNotFound 表示下载任务不存在。
var ErrDownloadNotFound = errors.New("下载任务不存在")

// DownloadTask 是一条持久化的下载任务。
//
// Link 唯一：同一条消息重复入队时返回已有任务，不重复下载。
type DownloadTask struct {
	ID        uint   `gorm:"primaryKey"`
	Link      string `gorm:"size:512;not null;uniqueIndex"`
	AccountID int64
	ChatID    int64
	ChatTitle string `gorm:"size:255"`
	MessageID int
	FileName  string `gorm:"size:255"`
	FileSize  int64
	Kind      string `gorm:"size:16"`
	MIME      string `gorm:"size:128"`

	Status   string `gorm:"size:16;index"`
	Offset   int64
	Speed    int64
	Error    string `gorm:"size:1024"`
	SavePath string `gorm:"size:1024"`
	// Overwrite 表示完成后直接覆盖同名文件（而不是加 " (1)" 后缀），
	// 并在开始前丢弃已有 .part 从零下载。
	Overwrite bool
	// ThumbnailURL 是创建任务时登记的缩略图地址（token 会过期）。
	ThumbnailURL string `gorm:"size:255"`
	// PreviewURL 是更大尺寸的预览图地址，供图片查看器使用。
	PreviewURL string `gorm:"size:255"`
	// ThumbKey / PreviewKey 是 thumbnail/ 文件缓存的键，可跨重启复用。
	ThumbKey   string `gorm:"size:64"`
	PreviewKey string `gorm:"size:64"`

	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName 固定表名，避免 GORM 复数化规则变化导致迁移出错。
func (DownloadTask) TableName() string { return "download_tasks" }

// dropLegacyThumbnailColumn 删除旧版本遗留的 download_tasks.thumbnail 列。
//
// 缩略图已改存 <数据目录>/thumbnail 文件缓存，列里的 BLOB 不再使用。
// SQLite 3.35+ 支持 DROP COLUMN，modernc 驱动自带的新版本满足要求；
// 列不存在（新库或已迁移过）时直接返回。
func (d *DB) dropLegacyThumbnailColumn() error {
	if !d.hasColumn("download_tasks", "thumbnail") {
		return nil
	}
	if err := d.Exec(`ALTER TABLE "download_tasks" DROP COLUMN "thumbnail"`).Error; err != nil {
		return fmt.Errorf("删除 download_tasks.thumbnail 列: %w", err)
	}
	return nil
}

// hasColumn 用 table_info 判断列是否存在（SQLite 专有）。
func (d *DB) hasColumn(table, column string) bool {
	var count int64
	err := d.Raw(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&count).Error
	return err == nil && count > 0
}

// CreateDownloadTask 插入任务；Link 已存在时返回已有任务且 created=false。
func (d *DB) CreateDownloadTask(task *DownloadTask) (created bool, err error) {
	result := d.Clauses(clause.OnConflict{DoNothing: true}).Create(task)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}

	// 冲突时把已有记录的 ID 回填给调用方，便于前端直接跳转。
	var existing DownloadTask
	if err := d.Where("link = ?", task.Link).First(&existing).Error; err != nil {
		return false, err
	}
	*task = existing
	return false, nil
}

// GetDownloadTask 按 ID 读取任务。
func (d *DB) GetDownloadTask(id uint) (DownloadTask, error) {
	var task DownloadTask
	if err := d.First(&task, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DownloadTask{}, ErrDownloadNotFound
		}
		return DownloadTask{}, err
	}
	return task, nil
}

// ListDownloadTasks 按状态筛选任务，按创建时间倒序返回；status 为空表示全部。
func (d *DB) ListDownloadTasks(status string, limit int) ([]DownloadTask, error) {
	if limit <= 0 {
		limit = 100
	}
	query := d.Order("id DESC").Limit(limit)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var tasks []DownloadTask
	if err := query.Find(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// UpdateDownloadTask 按 ID 更新指定字段；任务不存在时返回 ErrDownloadNotFound。
func (d *DB) UpdateDownloadTask(id uint, fields map[string]any) error {
	result := d.Model(&DownloadTask{}).Where("id = ?", id).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// RowsAffected 为 0 也可能只是值没变，用一次查询区分「不存在」。
		if _, err := d.GetDownloadTask(id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteDownloadTask 删除任务记录。
func (d *DB) DeleteDownloadTask(id uint) error {
	result := d.Delete(&DownloadTask{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDownloadNotFound
	}
	return nil
}

// ResetRunningDownloads 在进程启动时把残留的 running 任务置为 paused：
// 上一个进程已退出，任务不可能还在下载，保留 offset 等待手动恢复。
func (d *DB) ResetRunningDownloads() error {
	return d.Model(&DownloadTask{}).
		Where("status = ?", DownloadStatusRunning).
		Updates(map[string]any{
			"status": DownloadStatusPaused,
			"speed":  0,
			"error":  "服务重启，任务已暂停",
		}).Error
}

// SetDownloadPaused 把运行中或等待中的任务置为暂停。
//
// 条件更新：任务刚好完成或已被删除时不做任何事，避免把 done 覆盖成 paused。
func (d *DB) SetDownloadPaused(id uint, reason string) error {
	return d.Model(&DownloadTask{}).
		Where("id = ? AND status IN ?", id, []string{DownloadStatusRunning, DownloadStatusPending}).
		Updates(map[string]any{
			"status": DownloadStatusPaused,
			"speed":  0,
			"error":  reason,
		}).Error
}

// NormalizeDownloadThumbnailURLs 修正历史数据里重复拼接的缩略图前缀。
//
// 旧版本生成任务封面地址时把 /api/v1/media/thumbnail/ 拼了两次，
// <img> 请求会 404、任务列表只剩占位图标。这里把存量收敛成单前缀；
// 已经正常的地址不匹配替换串，保持原样。
func (d *DB) NormalizeDownloadThumbnailURLs() error {
	const (
		duplicated = "/api/v1/media/thumbnail//api/v1/media/thumbnail/"
		normal     = "/api/v1/media/thumbnail/"
	)
	return d.Model(&DownloadTask{}).
		Where("thumbnail_url LIKE ? OR preview_url LIKE ?", duplicated+"%", duplicated+"%").
		Updates(map[string]any{
			"thumbnail_url": gorm.Expr("REPLACE(thumbnail_url, ?, ?)", duplicated, normal),
			"preview_url":   gorm.Expr("REPLACE(preview_url, ?, ?)", duplicated, normal),
		}).Error
}
