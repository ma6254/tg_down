package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gotd/td/tg"
)

// LiveInfo 是联网查询到的账号资料与已登录客户端。
type LiveInfo struct {
	Account *Account
	Profile *ProfileInfo
	Devices []Device
}

// Inspect 联网查询账号资料（Self + users.getFullUser）与已登录客户端。
func (s Session) Inspect(ctx context.Context) (*LiveInfo, error) {
	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return nil, errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		return nil, fmt.Errorf("未找到 session 文件 %s，请先登录", opts.SessionPath)
	}

	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}

	var info *LiveInfo
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return errors.New("session 已失效，请重新执行 account login")
		}

		user, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("读取账号信息: %w", err)
		}
		account := &Account{
			ID:        user.ID,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Username:  user.Username,
			Phone:     user.Phone,
			Bot:       user.Bot,
			Premium:   user.Premium,
		}

		full, err := client.API().UsersGetFullUser(ctx, &tg.InputUserSelf{})
		if err != nil {
			return friendlyError(err)
		}

		// 设备列表是附加信息，失败时不阻断资料展示。
		devices := make([]Device, 0, 4)
		if auths, authErr := client.API().AccountGetAuthorizations(ctx); authErr == nil {
			for _, authorization := range auths.Authorizations {
				devices = append(devices, deviceFromTG(authorization))
			}
		}

		info = &LiveInfo{
			Account: account,
			Profile: profileFromFull(full),
			Devices: devices,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("未取到账号资料")
	}
	return info, nil
}

// deviceFromTG 把 Telegram 的 authorization 转成本地结构。
func deviceFromTG(a tg.Authorization) Device {
	return Device{
		Current:       a.Current,
		AppName:       a.AppName,
		AppVersion:    a.AppVersion,
		Platform:      a.Platform,
		DeviceModel:   a.DeviceModel,
		SystemVersion: a.SystemVersion,
		Country:       a.Country,
		CreatedAt:     unixTime(a.DateCreated),
		ActiveAt:      unixTime(a.DateActive),
	}
}
