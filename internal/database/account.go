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

	// AccountSourceManual 表示手机号/验证码手动登录。
	AccountSourceManual = "manual"
	// AccountSourceDesktop 表示从 Telegram Desktop 的 tdata 探测导入。
	AccountSourceDesktop = "desktop"
	// AccountSourceBot 表示 Bot token 接入（预留）。
	AccountSourceBot = "bot"
)

// Account 是本地保存的 Telegram 账号。
//
// SessionPath 指向 session 文件（mtproto）或留空（botapi）；
// token 等敏感字段不在这里明文存储，后续按需扩展。
type Account struct {
	ID   int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	Kind string `gorm:"size:16;index;not null" json:"kind"`
	// Source 记录账号来源：manual（手动登录）/ desktop（桌面导入）/ bot。
	// 旧数据为空，前端显示为“未记录”。
	Source      string `gorm:"size:16;index" json:"source"`
	Name        string `gorm:"size:120" json:"name"`
	Phone       string `gorm:"size:32;index" json:"phone"`
	Username    string `gorm:"size:64" json:"username"`
	SessionPath string `gorm:"size:255" json:"session_path"`
	State       string `gorm:"size:16;index" json:"state"`
	LastError   string `gorm:"type:text" json:"last_error"`
	// TelegramID 是 Telegram 的 user.id，跨手机号/用户名变化保持不变。
	// 用指针是为了让旧记录保持 NULL：SQLite 的唯一索引允许多个 NULL，
	// 但只允许一个 0，因此不能直接用零值表示"未绑定"。
	TelegramID *int64    `gorm:"column:tg_user_id;uniqueIndex" json:"-"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// TelegramIDValue 返回绑定的 Telegram 用户 ID；未绑定时为 0。
func (a Account) TelegramIDValue() int64 {
	if a.TelegramID == nil {
		return 0
	}
	return *a.TelegramID
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

// FindAccountByTelegramID 按 Telegram user.id 查询账号；未绑定时返回 gorm.ErrRecordNotFound。
func (d *DB) FindAccountByTelegramID(telegramID int64) (*Account, error) {
	var item Account
	if err := d.DB.Where("tg_user_id = ?", telegramID).First(&item).Error; err != nil {
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
