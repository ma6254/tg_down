package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Device 描述一个已登录的客户端会话。
type Device struct {
	Current       bool      // 是否为当前正在使用的会话
	AppName       string    // 应用名，例如 Telegram Desktop
	AppVersion    string    // 应用版本
	Platform      string    // 平台，例如 Windows / Android
	DeviceModel   string    // 设备型号
	SystemVersion string    // 系统版本
	Country       string    // 登录地国家
	CreatedAt     time.Time // 登录时间
	ActiveAt      time.Time // 最近活跃时间
}

// SessionInfo 是探测已有 session 的结果。
type SessionInfo struct {
	Authorized bool     // session 是否仍然有效
	Account    *Account // 授权时才有值
	Devices    []Device // 账号当前已登录的客户端列表
}

// Probe 用本地已有 session 连接 Telegram，读取账号信息与已登录客户端列表。
//
// 不会触发发码或登录流程；session 不存在或已失效时返回明确错误或 Authorized=false。
func Probe(ctx context.Context, opts LoginOptions) (*SessionInfo, error) {
	if opts.APIID <= 0 {
		return nil, errors.New("缺少 api_id：探测复用登录凭据，需提供 --api-id 或 TG_DOWN_API_ID")
	}
	if opts.APIHash == "" {
		return nil, errors.New("缺少 api_hash：需提供 --api-hash 或 TG_DOWN_API_HASH")
	}
	if opts.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	p := newPrompter(opts)
	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	if opts.Proxy != "" {
		fmt.Fprintf(p.out, "使用代理 %s 连接 Telegram\n", maskProxy(opts.Proxy))
	}

	info := &SessionInfo{}
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		info.Authorized = status.Authorized
		if !status.Authorized {
			return nil
		}

		user, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("读取账号信息: %w", err)
		}
		info.Account = &Account{
			ID:        user.ID,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Username:  user.Username,
			Phone:     user.Phone,
			Bot:       user.Bot,
			Premium:   user.Premium,
		}

		auths, err := client.API().AccountGetAuthorizations(ctx)
		if err != nil {
			// 客户端列表只是附加信息，失败时不影响账号探测结果。
			fmt.Fprintf(p.out, "提示：读取已登录客户端失败: %v\n", err)
			return nil
		}
		for _, a := range auths.Authorizations {
			info.Devices = append(info.Devices, Device{
				Current:       a.Current,
				AppName:       a.AppName,
				AppVersion:    a.AppVersion,
				Platform:      a.Platform,
				DeviceModel:   a.DeviceModel,
				SystemVersion: a.SystemVersion,
				Country:       a.Country,
				CreatedAt:     unixTime(a.DateCreated),
				ActiveAt:      unixTime(a.DateActive),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return info, nil
}

// unixTime 把秒级时间戳转成 time.Time，0 表示未知。
func unixTime(sec int) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0)
}
