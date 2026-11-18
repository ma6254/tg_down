package database

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// 账号类型与状态。
const (
	AccountKindMTProto = "mtproto"
	AccountKindBotAPI  = "botapi"

	AccountStateAuthed   = "authed"
	AccountStateUnAuthed = "unauthed"
	AccountStateError    = "error"
)

// Account 是本地保存的 Telegram 账号。
//
// SessionPath 指向 session 文件（mtproto）或留空（botapi）；
// token 等敏感字段不在这里明文存储，后续按需扩展。
type Account struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Kind        string    `gorm:"size:16;index;not null" json:"kind"`
	Name        string    `gorm:"size:120" json:"name"`
	Phone       string    `gorm:"size:32;index" json:"phone"`
	Username    string    `gorm:"size:64" json:"username"`
	SessionPath string    `gorm:"size:255" json:"session_path"`
	State       string    `gorm:"size:16;index" json:"state"`
	LastError   string    `gorm:"type:text" json:"last_error"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName 指定表名。
func (Account) TableName() string { return "accounts" }

// SaveAccount 新增或更新账号。
func (d *DB) SaveAccount(account *Account) error {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	return d.DB.Save(account).Error
}

// ListAccounts 按创建时间倒序返回全部账号。
func (d *DB) ListAccounts() ([]Account, error) {
	var items []Account
	if err := d.DB.Order("created_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// GetAccount 按 ID 查询账号。
func (d *DB) GetAccount(id int64) (*Account, error) {
	var item Account
	err := d.DB.First(&item, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, err
	}
	return &item, nil
}

// FindAccountByPhone 按手机号查询账号，未找到时返回 gorm.ErrRecordNotFound。
func (d *DB) FindAccountByPhone(phone string) (*Account, error) {
	var item Account
	if err := d.DB.Where("phone = ?", phone).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// FindAccountBySessionPath 按 session 文件路径查询账号。
func (d *DB) FindAccountBySessionPath(path string) (*Account, error) {
	var item Account
	if err := d.DB.Where("session_path = ?", path).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// CountAccounts 返回账号总数。
func (d *DB) CountAccounts() (int64, error) {
	var count int64
	if err := d.DB.Model(&Account{}).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// DeleteAccount 删除账号。
func (d *DB) DeleteAccount(id int64) error {
	return d.DB.Delete(&Account{}, id).Error
}
