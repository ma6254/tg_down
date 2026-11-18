package mtproto

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/session"
	"github.com/gotd/td/session/tdesktop"
	"github.com/gotd/td/telegram"
)

// DesktopProbeOptions 是探测本机 Telegram Desktop 的参数。
type DesktopProbeOptions struct {
	TDataPath string // tdata 目录，留空自动查找
	Passcode  []byte // 本地密码；未设置时留空
	Proxy     string // 出站代理
	Out       io.Writer
}

// DesktopAccount 是 tdata 中单个账号的探测结果。
type DesktopAccount struct {
	Index   uint32 // Telegram Desktop 内部账号序号
	UserID  uint64 // tdata 中记录的账号 ID
	MainDC  int    // 主 DC
	Account *Account
	Devices []Device
	// Session 是探测成功时从 tdata 转换出的授权，可用于导入到本工具。
	Session *session.Data
	Err     error
}

// DesktopProbeResult 是一次 tdata 探测的汇总。
type DesktopProbeResult struct {
	TDataPath string
	Accounts  []DesktopAccount
}

// ProbeDesktop 读取本机 Telegram Desktop 的 tdata，并用其中的授权连接到 Telegram
// 查询账号信息与已登录客户端。
func ProbeDesktop(ctx context.Context, opts DesktopProbeOptions) (*DesktopProbeResult, error) {
	root, err := locateTData(opts.TDataPath)
	if err != nil {
		return nil, err
	}

	accounts, err := tdesktop.Read(root, opts.Passcode)
	if err != nil {
		return nil, fmt.Errorf("解析 tdata: %w（若 Telegram Desktop 设置了本地密码，请用 --tdata-passcode 提供）", err)
	}
	if len(accounts) == 0 {
		return nil, errors.New("tdata 中没有找到已登录的账号")
	}

	result := &DesktopProbeResult{TDataPath: root}
	for _, account := range accounts {
		item := DesktopAccount{
			Index:  account.IDx,
			UserID: account.Authorization.UserID,
			MainDC: account.Authorization.MainDC,
		}

		data, err := session.TDesktopSession(account)
		if err != nil {
			item.Err = fmt.Errorf("转换 session: %w", err)
			result.Accounts = append(result.Accounts, item)
			continue
		}

		info, devices, err := probeWithData(ctx, data, opts)
		if err != nil {
			item.Err = err
			result.Accounts = append(result.Accounts, item)
			continue
		}
		item.Account = info
		item.Devices = devices
		item.Session = data
		result.Accounts = append(result.Accounts, item)
	}

	return result, nil
}

// probeWithData 用给定的 session 数据建立只读连接并查询账号信息。
func probeWithData(ctx context.Context, data *session.Data, opts DesktopProbeOptions) (*Account, []Device, error) {
	var storage session.StorageMemory
	loader := &session.Loader{Storage: &storage}
	if err := loader.Save(ctx, data); err != nil {
		return nil, nil, fmt.Errorf("装载 session: %w", err)
	}

	app, err := PresetApp(AppDesktop)
	if err != nil {
		return nil, nil, err
	}
	// tdata 中的授权由 Telegram Desktop 建立，连接时沿用同一个应用身份。
	client, err := newClientWithStorage(&storage, app, opts.Proxy)
	if err != nil {
		return nil, nil, err
	}

	var (
		account *Account
		devices []Device
	)
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return errors.New("会话已失效（可能是其他客户端主动退出，或授权已过期）")
		}

		user, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("读取账号信息: %w", err)
		}
		account = &Account{
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
			return nil
		}
		for _, a := range auths.Authorizations {
			devices = append(devices, Device{
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
		return nil, nil, err
	}
	return account, devices, nil
}

// locateTData 返回 Telegram Desktop 的 tdata 目录：显式指定优先，否则扫描常见安装位置。
func locateTData(explicit string) (string, error) {
	if explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("tdata 目录 %q 不可用: %w", explicit, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("--tdata %q 不是目录", explicit)
		}
		return explicit, nil
	}

	candidates := tdataCandidates()
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "key_datas")); err == nil {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 Telegram Desktop 的 tdata 目录，已尝试：\n  %s\n可用 --tdata 手动指定",
		strings.Join(candidates, "\n  "))
}

// tdataCandidates 返回常见的 tdata 位置。
func tdataCandidates() []string {
	var dirs []string
	if appData := os.Getenv("APPDATA"); appData != "" {
		dirs = append(dirs,
			filepath.Join(appData, "Telegram Desktop", "tdata"),
			filepath.Join(appData, "TelegramDesktop", "tdata"),
		)
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		dirs = append(dirs, filepath.Join(localAppData, "Telegram Desktop", "tdata"))
	}
	return dirs
}

// newClientWithStorage 用指定 session 存储与固定应用构造客户端。
func newClientWithStorage(storage telegram.SessionStorage, app App, proxyURL string) (*telegram.Client, error) {
	dial, err := newProxyDialer(proxyURL)
	if err != nil {
		return nil, err
	}
	clientOpts := telegram.Options{
		SessionStorage: storage,
		NoUpdates:      true,
	}
	if dial != nil {
		clientOpts.Resolver = dcsPlain(dial)
	}
	return telegram.NewClient(app.ID, app.Hash, clientOpts), nil
}

// SaveSessionFile 把探测到的授权写成 session 文件（gotd JSON 格式，权限 0600）。
func SaveSessionFile(path string, data *session.Data) error {
	if data == nil {
		return errors.New("缺少 session 数据")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建 session 目录: %w", err)
		}
	}

	loader := &session.Loader{Storage: &session.FileStorage{Path: path}}
	if err := loader.Save(context.Background(), data); err != nil {
		return fmt.Errorf("写入 session: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置 session 权限: %w", err)
	}
	return nil
}
