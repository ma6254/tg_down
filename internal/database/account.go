package database

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/appdir"
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
//
// 写库前把 session_path 相对化到数据目录：数据库整体搬走后路径仍然有效。
// 调用方（以及随后读出的 Account）拿到的是真实文件路径。
func (d *DB) SaveAccount(account *Account) error {
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now()
	}
	row := *account
	row.SessionPath = appdir.RelSessionPath(account.SessionPath)
	if err := d.DB.Save(&row).Error; err != nil {
		return err
	}
	// 回填自增 ID 与时间戳；内存里的 SessionPath 保持真实路径。
	account.ID = row.ID
	account.CreatedAt = row.CreatedAt
	account.UpdatedAt = row.UpdatedAt
	return nil
}

// resolveSessionPath 把库里的相对路径还原成真实文件路径。
func (a *Account) resolveSessionPath() {
	a.SessionPath = appdir.AbsSessionPath(a.SessionPath)
}

// ListAccounts 按创建时间倒序返回全部账号。
func (d *DB) ListAccounts() ([]Account, error) {
	var items []Account
	if err := d.DB.Order("created_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	for i := range items {
		items[i].resolveSessionPath()
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
	item.resolveSessionPath()
	return &item, nil
}

// FindAccountByPhone 按手机号查询账号，未找到时返回 gorm.ErrRecordNotFound。
func (d *DB) FindAccountByPhone(phone string) (*Account, error) {
	var item Account
	if err := d.DB.Where("phone = ?", phone).First(&item).Error; err != nil {
		return nil, err
	}
	item.resolveSessionPath()
	return &item, nil
}

// FindAccountBySessionPath 按 session 文件路径查询账号。
//
// 入参可以是绝对路径，也可以是从库里读出的相对路径；统一按相对形式匹配。
func (d *DB) FindAccountBySessionPath(path string) (*Account, error) {
	var item Account
	if err := d.DB.Where("session_path = ?", appdir.RelSessionPath(path)).First(&item).Error; err != nil {
		return nil, err
	}
	item.resolveSessionPath()
	return &item, nil
}

// FindAccountByTelegramID 按 Telegram user.id 查询账号；未绑定时返回 gorm.ErrRecordNotFound。
func (d *DB) FindAccountByTelegramID(telegramID int64) (*Account, error) {
	var item Account
	if err := d.DB.Where("tg_user_id = ?", telegramID).First(&item).Error; err != nil {
		return nil, err
	}
	item.resolveSessionPath()
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

// NormalizeSessionPaths 把历史数据里存成绝对路径的 session_path 改成相对路径。
//
// 读取本来就兼容两种形式；这里只是把库里的存量统一成"相对数据目录"，
// 让数据目录整体搬走后路径不再失效。重复执行无副作用。
func (d *DB) NormalizeSessionPaths() error {
	var rows []Account
	if err := d.DB.Select("id", "session_path").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if row.SessionPath == "" {
			continue
		}
		rel := appdir.RelSessionPath(row.SessionPath)
		if rel == row.SessionPath {
			continue
		}
		if err := d.DB.Model(&Account{}).Where("id = ?", row.ID).
			Update("session_path", rel).Error; err != nil {
			return err
		}
	}
	return nil
}
