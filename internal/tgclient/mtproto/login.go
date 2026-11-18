// Package mtproto 封装 MTProto 用户账号的登录与会话持久化。
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
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
)

// Account 是登录成功后返回的账号信息，只包含展示所需字段。
type Account struct {
	ID        int64
	FirstName string
	LastName  string
	Username  string
	Phone     string
	Bot       bool
	Premium   bool
}

// DisplayName 返回展示名，依次退化到姓名、用户名、ID。
func (a *Account) DisplayName() string {
	if name := strings.TrimSpace(a.FirstName + " " + a.LastName); name != "" {
		return name
	}
	if a.Username != "" {
		return "@" + a.Username
	}
	return fmt.Sprintf("user:%d", a.ID)
}

// LoginOptions 是 MTProto 登录参数。
type LoginOptions struct {
	APIID       int    // my.telegram.org 申请的 api_id
	APIHash     string // my.telegram.org 申请的 api_hash
	Phone       string // E.164 手机号
	Code        string // 验证码，留空则交互输入
	Password    string // 两步验证密码，留空则按需交互输入
	SessionPath string // session 文件路径
	Proxy       string // 出站代理 URL，空表示直连
	In          io.Reader
	Out         io.Writer
}

// Login 完成 MTProto 登录，并把 session 持久化到 SessionPath。
//
// 若 session 已授权则跳过验证码，直接返回当前账号，可安全重复调用。
func Login(ctx context.Context, opts LoginOptions) (*Account, error) {
	if opts.APIID <= 0 {
		return nil, errors.New("缺少 api_id：请到 https://my.telegram.org 申请，或用 --api-id / TG_DOWN_API_ID 传入")
	}
	if strings.TrimSpace(opts.APIHash) == "" {
		return nil, errors.New("缺少 api_hash：请到 https://my.telegram.org 申请，或用 --api-hash / TG_DOWN_API_HASH 传入")
	}
	if strings.TrimSpace(opts.Phone) == "" {
		return nil, errors.New("缺少手机号")
	}
	if strings.TrimSpace(opts.SessionPath) == "" {
		return nil, errors.New("缺少 session 路径")
	}

	if dir := filepath.Dir(opts.SessionPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建 session 目录: %w", err)
		}
	}

	p := newPrompter(opts)
	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	if opts.Proxy != "" {
		fmt.Fprintf(p.out, "使用代理 %s 连接 Telegram\n", maskProxy(opts.Proxy))
	}

	var account *Account
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}

		if status.Authorized {
			fmt.Fprintln(p.out, "检测到有效 session，跳过验证码登录")
		} else {
			fmt.Fprintf(p.out, "正在向 %s 发送登录验证码…\n", maskPhone(opts.Phone))
			if err := client.Auth().IfNecessary(ctx, auth.NewFlow(p, auth.SendCodeOptions{})); err != nil {
				return friendlyError(err)
			}
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
		return nil
	})
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, errors.New("登录流程结束但未取得账号信息")
	}

	// session 等同于账号凭证，尽量收紧权限（Windows 上 chmod 语义有限）。
	if err := os.Chmod(opts.SessionPath, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(p.out, "警告：收紧 session 文件权限失败: %v\n", err)
	}

	return account, nil
}

// maskPhone 把手机号中间部分打码，避免日志与终端出现完整号码。
func maskPhone(phone string) string {
	if len(phone) <= 7 {
		return phone
	}
	return phone[:4] + "****" + phone[len(phone)-3:]
}

// newClient 按统一配置构造 MTProto 客户端：文件 session 存储与出站代理。
func newClient(opts LoginOptions) (*telegram.Client, error) {
	app := App{ID: opts.APIID, Hash: opts.APIHash}
	return newClientWithStorage(&session.FileStorage{Path: opts.SessionPath}, app, opts.Proxy)
}
