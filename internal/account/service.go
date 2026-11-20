// Package account 实现账号的持久化与导入逻辑，不依赖命令行。
package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// Service 是账号相关的业务逻辑，由命令层用数据库句柄构造。
type Service struct {
	db *database.DB
}

// New 创建账号服务。
func New(db *database.DB) *Service {
	return &Service{db: db}
}

// RecordInput 是登记账号所需的字段。
type RecordInput struct {
	Kind        string
	Source      string
	Name        string
	Phone       string
	Username    string
	TelegramID  int64
	SessionPath string
}

// Record 优先按 Telegram user.id 去重，其次按 session 路径。
//
// 手机号与用户名都可能变化：同一 Telegram 账号换绑手机号后 session 路径会变，
// 只有 user.id 不变，所以拿到它时应以它为准。
func (s *Service) Record(in RecordInput) (*database.Account, error) {
	var row *database.Account

	if in.TelegramID > 0 {
		existing, err := s.db.FindAccountByTelegramID(in.TelegramID)
		switch {
		case err == nil:
			row = existing
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 继续按 session 路径兜底。
		default:
			return nil, err
		}
	}

	if row == nil {
		existing, err := s.db.FindAccountBySessionPath(in.SessionPath)
		switch {
		case err == nil:
			// 已存在：更新可变字段。
			row = existing
		case errors.Is(err, gorm.ErrRecordNotFound):
			row = &database.Account{
				Kind:        in.Kind,
				SessionPath: in.SessionPath,
			}
		default:
			return nil, err
		}
	}

	row.Kind = in.Kind
	if in.Source != "" {
		row.Source = in.Source
	}
	row.Name = in.Name
	row.Phone = in.Phone
	row.Username = in.Username
	row.SessionPath = in.SessionPath
	row.State = database.AccountStateAuthed
	row.LastError = ""
	if in.TelegramID > 0 {
		id := in.TelegramID
		row.TelegramID = &id
	}

	if err := s.db.SaveAccount(row); err != nil {
		return nil, err
	}
	return row, nil
}

// BindTelegramID 把联网查到的 user.id 绑定到本地账号。
//
// 已被另一条账号记录占用时不做改动（避免唯一索引冲突），返回 nil。
func (s *Service) BindTelegramID(accountID, telegramID int64) error {
	if telegramID <= 0 {
		return nil
	}
	row, err := s.db.GetAccount(accountID)
	if err != nil {
		return err
	}
	if row.TelegramIDValue() == telegramID {
		return nil
	}
	if other, err := s.db.FindAccountByTelegramID(telegramID); err == nil && other.ID != accountID {
		return nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	id := telegramID
	row.TelegramID = &id
	return s.db.SaveAccount(row)
}

// AutoSelectSingle 在库里只有一个账号时把它设为当前账号。
func (s *Service) AutoSelectSingle() (bool, error) {
	count, err := s.db.CountAccounts()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}

	accounts, err := s.db.ListAccounts()
	if err != nil {
		return false, err
	}
	if len(accounts) != 1 {
		return false, nil
	}

	store := config.NewStore(s.db)
	current, ok, err := store.CurrentAccountID()
	if err != nil {
		return false, err
	}
	if ok && current == accounts[0].ID {
		return false, nil
	}
	if err := store.SetCurrentAccountID(accounts[0].ID); err != nil {
		return false, err
	}
	return true, nil
}

// ImportResult 是一次桌面导入的结果。
type ImportResult struct {
	Imported int // 新写入 session 文件的账号数
	Skipped  int // session 文件已存在、被保留的账号数
}

// ImportDesktop 把探测到的桌面账号写入 session 文件与数据库。
func (s *Service) ImportDesktop(sessionDir string, result *mtproto.DesktopProbeResult) (ImportResult, error) {
	var out ImportResult
	for _, item := range result.Accounts {
		if item.Err != nil || item.Account == nil {
			continue
		}

		phone := strings.TrimSpace(item.Account.Phone)
		if phone == "" {
			continue
		}
		sessionPath := filepath.Join(sessionDir, "mtproto-"+utils.DigitsOnly(phone)+".json")

		if _, statErr := os.Stat(sessionPath); statErr == nil {
			out.Skipped++
		} else {
			if err := mtproto.SaveSessionFile(sessionPath, item.Session); err != nil {
				return out, fmt.Errorf("保存 session %s: %w", sessionPath, err)
			}
			out.Imported++
		}

		name := item.Account.DisplayName()
		if _, err := s.Record(RecordInput{
			Kind:        database.AccountKindMTProto,
			Source:      database.AccountSourceDesktop,
			Name:        name,
			Phone:       phone,
			Username:    item.Account.Username,
			TelegramID:  item.Account.ID,
			SessionPath: sessionPath,
		}); err != nil {
			return out, fmt.Errorf("写入账号数据库: %w", err)
		}
	}
	return out, nil
}

// List 返回全部账号。
func (s *Service) List() ([]database.Account, error) {
	return s.db.ListAccounts()
}

// Get 按 ID 查询账号。
func (s *Service) Get(id int64) (*database.Account, error) {
	return s.db.GetAccount(id)
}

// CurrentAccountID 返回当前选中的账号 ID。
func (s *Service) CurrentAccountID() (int64, bool, error) {
	return config.NewStore(s.db).CurrentAccountID()
}

// App 读取 settings 里的自定义 Telegram 应用凭据。
//
// 只有 api_custom_enabled=true 时才使用自定义凭据；关闭或未配置时
// custom 为 false，调用方应回退内置 desktop。启用后如果两项凭据缺失或
// 只配置了一项，返回错误而不是用半个凭据去连接 Telegram。
func (s *Service) App() (app mtproto.App, custom bool, err error) {
	store := config.NewStore(s.db)

	enabled, err := store.Bool(config.KeyAPICustomEnabled)
	if err != nil {
		return mtproto.App{}, false, fmt.Errorf("读取 api_custom_enabled: %w", err)
	}
	if !enabled {
		return mtproto.App{}, false, nil
	}

	idValue, hasID, err := store.Get(config.KeyAPIID)
	if err != nil {
		return mtproto.App{}, false, fmt.Errorf("读取 api_id: %w", err)
	}
	hashValue, hasHash, err := store.Get(config.KeyAPIHash)
	if err != nil {
		return mtproto.App{}, false, fmt.Errorf("读取 api_hash: %w", err)
	}

	idValue = strings.TrimSpace(idValue)
	hashValue = strings.TrimSpace(hashValue)
	if !hasID && !hasHash {
		return mtproto.App{}, false, errors.New("已启用自定义 api 凭据（api_custom_enabled=true），但 api_id / api_hash 未配置")
	}
	if idValue == "" || hashValue == "" {
		return mtproto.App{}, false, errors.New("已启用自定义 api 凭据，api_id 与 api_hash 需要同时配置")
	}

	id, err := strconv.Atoi(idValue)
	if err != nil || id <= 0 {
		return mtproto.App{}, false, fmt.Errorf("settings 里的 api_id 无效：%q", idValue)
	}
	return mtproto.App{ID: id, Hash: hashValue}, true, nil
}

// AppOrDefault 返回最终使用的应用凭据：配置了自定义凭据就用自定义，
// 否则回退内置 desktop（api_id 2040）。
func (s *Service) AppOrDefault() (mtproto.App, error) {
	app, custom, err := s.App()
	if err != nil {
		return mtproto.App{}, err
	}
	if custom {
		return app, nil
	}
	return mtproto.PresetApp(mtproto.AppDesktop)
}

// SetCurrentAccountID 设置当前选中的账号 ID。
func (s *Service) SetCurrentAccountID(id int64) error {
	return config.NewStore(s.db).SetCurrentAccountID(id)
}

// FindByPhoneOrName 按手机号、名称或用户名查找账号。
func (s *Service) FindByPhoneOrName(key string) (*database.Account, error) {
	accounts, err := s.db.ListAccounts()
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	for i := range accounts {
		account := accounts[i]
		if account.Name == key || account.Username == key || account.Phone == key {
			return &account, nil
		}
		if account.Phone != "" && utils.DigitsOnly(account.Phone) == utils.DigitsOnly(key) {
			return &account, nil
		}
	}
	return nil, fmt.Errorf("没有找到匹配 %q 的账号", key)
}

// SelectForLogout 解析要删除的账号：全部、按 ID 或按名称/手机号。
func (s *Service) SelectForLogout(id int64, name string, all bool) ([]database.Account, error) {
	if all {
		return s.db.ListAccounts()
	}
	if id > 0 {
		account, err := s.db.GetAccount(id)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("账号 ID %d 不存在", id)
			}
			return nil, err
		}
		return []database.Account{*account}, nil
	}

	account, err := s.FindByPhoneOrName(name)
	if err != nil {
		return nil, err
	}
	return []database.Account{*account}, nil
}

// Delete 删除账号记录与 session 文件；若删除的是当前账号会清空选择。
func (s *Service) Delete(account database.Account) (removedFile bool, err error) {
	if err := s.db.DeleteAccount(account.ID); err != nil {
		return false, err
	}

	if strings.TrimSpace(account.SessionPath) != "" {
		if _, statErr := os.Stat(account.SessionPath); statErr == nil {
			removedFile = os.Remove(account.SessionPath) == nil
		}
	}

	store := config.NewStore(s.db)
	if current, ok, curErr := store.CurrentAccountID(); curErr == nil && ok && current == account.ID {
		_ = store.ClearCurrentAccountID()
	}
	return removedFile, nil
}
