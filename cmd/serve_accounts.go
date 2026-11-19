package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// listAccounts 读取数据库里的账号并标出当前选中项，等价于 CLI 的 account list。
func listAccounts(ctx context.Context) ([]server.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	svc := accountsvc.New(db)
	rows, err := svc.List()
	if err != nil {
		return nil, err
	}

	current, hasCurrent, err := svc.CurrentAccountID()
	if err != nil {
		return nil, err
	}

	out := make([]server.Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, server.Account{
			ID:        row.ID,
			Kind:      row.Kind,
			Name:      row.Name,
			Username:  row.Username,
			Phone:     row.Phone,
			State:     row.State,
			LastError: row.LastError,
			CreatedAt: row.CreatedAt.Unix(),
			Current:   hasCurrent && current == row.ID,
		})
	}
	return out, nil
}

// selectAccount 把指定账号设为当前账号，等价于 CLI 的 account switch <id>。
//
// 切换前确认 session 文件存在，避免切到一个不可用的账号；
// 账号不存在 / session 缺失分别用 server 包的两个哨兵错误返回。
func selectAccount(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: 账号 ID %d 不存在，可用 GET /api/v1/accounts 查看", server.ErrAccountNotFound, id)
		}
		return fmt.Errorf("查询账号 %d: %w", id, err)
	}

	if strings.TrimSpace(account.SessionPath) == "" {
		return fmt.Errorf("%w: 账号 #%d 没有 session 文件，请重新登录", server.ErrAccountNoSession, account.ID)
	}
	if info, statErr := os.Stat(account.SessionPath); statErr != nil || info.IsDir() {
		return fmt.Errorf("%w: 账号 #%d 的 session 文件 %s 不存在，请重新登录", server.ErrAccountNoSession, account.ID, account.SessionPath)
	}

	if err := config.NewStore(db).SetCurrentAccountID(account.ID); err != nil {
		return fmt.Errorf("保存当前账号: %w", err)
	}
	return nil
}

// getAccount 返回单个账号的详情，等价于 CLI 的 account info <id>。
//
// 只读本地数据库，不联网；session 文件是否存在单独用 session_present 标记。
func getAccount(ctx context.Context, id int64) (server.AccountDetail, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountDetail{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountDetail{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return server.AccountDetail{}, fmt.Errorf("%w: 账号 ID %d 不存在，可用 GET /api/v1/accounts 查看", server.ErrAccountNotFound, id)
		}
		return server.AccountDetail{}, fmt.Errorf("查询账号 %d: %w", id, err)
	}

	current, hasCurrent, err := config.NewStore(db).CurrentAccountID()
	if err != nil {
		return server.AccountDetail{}, fmt.Errorf("读取当前账号: %w", err)
	}

	detail := server.AccountDetail{
		Account: server.Account{
			ID:        account.ID,
			Kind:      account.Kind,
			Name:      account.Name,
			Username:  account.Username,
			Phone:     account.Phone,
			State:     account.State,
			LastError: account.LastError,
			CreatedAt: account.CreatedAt.Unix(),
			Current:   hasCurrent && current == account.ID,
		},
		SessionPresent: sessionFileExists(account.SessionPath),
	}
	if !account.UpdatedAt.IsZero() {
		detail.UpdatedAt = account.UpdatedAt.Unix()
	}
	return detail, nil
}

// getAccountLive 联网查询账号的最新资料与已登录客户端，等价于 CLI 的
// account info <id> --live。结果只在内存里返回，不写数据库。
//
// 与切换账号一样先检查 session 文件：没有它连不上 Telegram，
// 直接返回 ErrAccountNoSession 比等超时更友好。
func getAccountLive(ctx context.Context, id int64) (server.AccountLiveInfo, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountLiveInfo{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountLiveInfo{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return server.AccountLiveInfo{}, fmt.Errorf("%w: 账号 ID %d 不存在，可用 GET /api/v1/accounts 查看", server.ErrAccountNotFound, id)
		}
		return server.AccountLiveInfo{}, fmt.Errorf("查询账号 %d: %w", id, err)
	}

	if !sessionFileExists(account.SessionPath) {
		return server.AccountLiveInfo{}, fmt.Errorf("%w: 账号 #%d 的 session 文件不存在，无法联网查询", server.ErrAccountNoSession, account.ID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return server.AccountLiveInfo{}, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := mtproto.PresetApp(mtproto.AppDesktop)
	if err != nil {
		return server.AccountLiveInfo{}, err
	}

	// 联网查询有明确上限：前端也会超时，长时间挂起没有意义。
	ctx, cancel := context.WithTimeout(ctx, utils.DefaultNetworkTimeout)
	defer cancel()

	session := mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}
	live, err := session.Inspect(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return server.AccountLiveInfo{}, err
		}
		return server.AccountLiveInfo{}, fmt.Errorf("%w: %v", server.ErrAccountLiveFailed, err)
	}

	info := server.AccountLiveInfo{
		Devices:   probeDevices(live.Devices),
		FetchedAt: time.Now().Unix(),
	}
	// 前端按数组遍历，空列表要返回 [] 而不是 null。
	if info.Devices == nil {
		info.Devices = []server.ProbeDevice{}
	}
	if live.Account != nil {
		info.UserID = live.Account.ID
		info.Name = live.Account.DisplayName()
		info.Username = live.Account.Username
		info.Phone = live.Account.Phone
		info.Bot = live.Account.Bot
		info.Premium = live.Account.Premium
	}
	if live.Profile != nil {
		info.About = live.Profile.About
		if live.Profile.HasBirthday {
			info.Birthday = &server.AccountBirthday{
				Day:   live.Profile.BirthDay,
				Month: live.Profile.BirthMonth,
				Year:  live.Profile.BirthYear,
			}
		}
	}
	return info, nil
}

// sessionFileExists 判断 session 路径是否指向一个存在的文件。
func sessionFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
