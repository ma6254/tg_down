package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Logout 注销当前 session：向 Telegram 发送 auth.logOut，并清空本地 session 文件。
//
// session 已失效或文件不存在时视为已经登出，返回 nil。
func (s Session) Logout(ctx context.Context) error {
	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 没有 session 文件，等于已经登出。
			return nil
		}
		return fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(opts)
	if err != nil {
		return err
	}

	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return nil
		}
		// LogOut 会同时清理本地 session 文件。
		if _, err := client.LogOut(ctx); err != nil {
			return friendlyError(err)
		}
		return nil
	})
}
