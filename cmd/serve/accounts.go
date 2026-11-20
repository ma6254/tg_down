package serve

import (
	"context"
	"errors"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/server"
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
	return server.NewAccounts(rows, current, hasCurrent), nil
}

// selectAccount 把指定账号设为当前账号，等价于 CLI 的 account switch <id>。
func selectAccount(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	return mapAccountError(accountsvc.New(db).Select(id))
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

	svc := accountsvc.New(db)
	row, err := svc.GetByID(id)
	if err != nil {
		return server.AccountDetail{}, mapAccountError(err)
	}
	current, hasCurrent, err := svc.CurrentAccountID()
	if err != nil {
		return server.AccountDetail{}, fmt.Errorf("读取当前账号: %w", err)
	}
	return server.NewAccountDetail(*row, hasCurrent && current == row.ID, utils.FileExists(row.SessionPath)), nil
}

// getAccountLive 联网查询账号的最新资料与已登录客户端，等价于 CLI 的
// account info <id> --live。
func getAccountLive(ctx context.Context, id int64) (server.AccountLiveInfo, error) {
	if err := ctx.Err(); err != nil {
		return server.AccountLiveInfo{}, err
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.AccountLiveInfo{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	live, err := accountsvc.New(db).Live(ctx, id)
	if err != nil {
		return server.AccountLiveInfo{}, mapAccountError(err)
	}
	return server.NewAccountLiveInfo(live), nil
}

// mapAccountError 把 internal/account 的哨兵错误翻译成 server 的错误。
//
// 领域层不认识 HTTP；接口层用 server 的哨兵错误决定状态码，
// 这里只做一次映射，其余错误原样上抛（handler 按 500 处理）。
func mapAccountError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, accountsvc.ErrNotFound):
		return fmt.Errorf("%w: %v", server.ErrAccountNotFound, err)
	case errors.Is(err, accountsvc.ErrNoSession):
		return fmt.Errorf("%w: %v", server.ErrAccountNoSession, err)
	case errors.Is(err, accountsvc.ErrLiveFailed):
		return fmt.Errorf("%w: %v", server.ErrAccountLiveFailed, err)
	case errors.Is(err, accountsvc.ErrNoAvatar):
		return fmt.Errorf("%w: %v", server.ErrThumbnailNotFound, err)
	case errors.Is(err, accountsvc.ErrDesktopCannotLogout):
		return fmt.Errorf("%w: %v", server.ErrAccountDesktopLogout, err)
	}
	return err
}
