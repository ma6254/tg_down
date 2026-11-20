package account

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// 账号用例的哨兵错误。命令层据此映射成 CLI 文案或 HTTP 状态码，
// 业务层本身不依赖任何传输方式。
var (
	// ErrNotFound 表示账号 ID 不存在。
	ErrNotFound = errors.New("账号不存在")
	// ErrNoSession 表示账号缺少可用的 session 文件（未登录或文件已丢）。
	ErrNoSession = errors.New("账号缺少 session 文件")
	// ErrLiveFailed 表示联网查询账号资料失败（网络、协议或凭据问题）。
	ErrLiveFailed = errors.New("联网查询账号失败")
	// ErrDesktopCannotLogout 表示桌面导入的账号只能删除，不能向 Telegram 注销。
	ErrDesktopCannotLogout = errors.New("桌面导入的账号不能注销")
)

// GetByID 按 ID 查询账号，账号不存在时返回 ErrNotFound。
//
// 与 Get 的区别：Get 原样返回 gorm 错误，供需要自己区分数据层错误的调用方使用；
// 命令层通常只关心「不存在」这一种情况。
func (s *Service) GetByID(id int64) (*database.Account, error) {
	row, err := s.db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: ID %d", ErrNotFound, id)
		}
		return nil, fmt.Errorf("查询账号 %d: %w", id, err)
	}
	return row, nil
}

// Select 把账号设为当前账号。
//
// 切换前确认 session 文件存在，避免切到一个连不上的账号。
func (s *Service) Select(id int64) error {
	row, err := s.GetByID(id)
	if err != nil {
		return err
	}
	if err := requireSession(*row); err != nil {
		return err
	}
	if err := s.SetCurrentAccountID(row.ID); err != nil {
		return fmt.Errorf("保存当前账号: %w", err)
	}
	return nil
}

// requireSession 确认账号的 session 文件存在。
func requireSession(row database.Account) error {
	if !utils.FileExists(row.SessionPath) {
		return fmt.Errorf("%w: 账号 #%d 的 session 文件不存在，请重新登录", ErrNoSession, row.ID)
	}
	return nil
}

// Live 联网查询账号的最新资料与已登录客户端，结果不落库。
//
// 拿到真实 user.id 后顺手绑定，后续去重不再依赖手机号或用户名。
func (s *Service) Live(ctx context.Context, id int64) (*mtproto.LiveInfo, error) {
	row, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if err := requireSession(*row); err != nil {
		return nil, err
	}

	proxy, err := config.NewStore(s.db).Proxy()
	if err != nil {
		return nil, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := s.AppOrDefault()
	if err != nil {
		return nil, err
	}

	// 联网查询有明确上限：前端也会超时，长时间挂起没有意义。
	callCtx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
	defer cancel()

	session := mtproto.Session{App: app, SessionPath: row.SessionPath, Proxy: proxy}
	live, err := session.Inspect(callCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrLiveFailed, err)
	}

	if live.Account != nil && live.Account.ID > 0 {
		_ = s.BindTelegramID(row.ID, live.Account.ID)
	}
	return live, nil
}

// LogoutResult 是一次退出登录的结果。
type LogoutResult struct {
	Revoked     bool // 是否成功吊销了 Telegram session
	RemovedFile bool // 是否删掉了 session 文件
	Removed     database.Account
}

// Logout 退出登录：尽力吊销 Telegram session，然后删除本地记录与 session 文件。
//
// 桌面导入的账号与 Telegram Desktop 共用 auth key，吊销会影响桌面端，
// 因此直接拒绝（ErrDesktopCannotLogout），调用方应改用 Remove。
// 联网失败不阻断本地清理：此时 Revoked=false，账号仍然被删除。
func (s *Service) Logout(ctx context.Context, id int64) (LogoutResult, error) {
	row, err := s.GetByID(id)
	if err != nil {
		return LogoutResult{}, err
	}
	if row.Source == database.AccountSourceDesktop {
		return LogoutResult{}, fmt.Errorf(
			"%w: 账号 #%d 由 Telegram Desktop 导入，请改用删除（只移除本地副本）", ErrDesktopCannotLogout, row.ID)
	}

	var out LogoutResult
	if row.Kind == database.AccountKindMTProto && utils.FileExists(row.SessionPath) {
		if proxy, proxyErr := config.NewStore(s.db).Proxy(); proxyErr == nil {
			if app, appErr := s.AppOrDefault(); appErr == nil {
				session := mtproto.Session{App: app, SessionPath: row.SessionPath, Proxy: proxy}
				callCtx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
				out.Revoked = session.Logout(callCtx) == nil
				cancel()
			}
		}
	}

	removedFile, err := s.Delete(*row)
	if err != nil {
		return LogoutResult{}, fmt.Errorf("删除账号 %d: %w", row.ID, err)
	}
	out.RemovedFile = removedFile
	out.Removed = *row
	return out, nil
}

// Remove 按 ID 删除本地账号记录与 session 文件，不向 Telegram 注销。
//
// 返回被删掉的账号（调用方据此清理头像缓存等附属文件）。
func (s *Service) Remove(id int64) (database.Account, bool, error) {
	row, err := s.GetByID(id)
	if err != nil {
		return database.Account{}, false, err
	}
	removedFile, err := s.Delete(*row)
	if err != nil {
		return *row, false, fmt.Errorf("删除账号 %d: %w", row.ID, err)
	}
	return *row, removedFile, nil
}
