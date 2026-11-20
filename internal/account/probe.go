package account

import (
	"context"
	"fmt"
	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// probeDesktop 是 tdata 探测实现；测试里替换成假数据，避免真的连接 Telegram。
var probeDesktop = mtproto.ProbeDesktop

// ProbeOptions 是 tdata 登录探测的输入。
//
// session 文件的落盘位置固定为数据目录下的 sessions（appdir.SessionsDir），
// 不再提供覆盖参数：CLI 的 --session-dir 走自己的导入流程，不会走到这里。
type ProbeOptions struct {
	// TDataPath 是 Telegram Desktop 的 tdata 目录，留空时自动查找。
	TDataPath string
	// Passcode 是 Telegram Desktop 的本地密码，空表示没有设置。
	Passcode string
	// Import 为 false 时只探测，不写 session 文件与数据库。
	Import bool
}

// ProbeAccount 是 tdata 中单个账号的探测结果。
//
// 单个账号失败不影响其他账号：失败原因放在 Err 里，为 nil 表示探测成功。
type ProbeAccount struct {
	Index    uint32
	UserID   uint64
	Name     string
	Username string
	Phone    string
	Devices  []mtproto.Device
	Err      error
}

// ProbeResult 是一次登录探测的结果。
type ProbeResult struct {
	TDataPath         string
	Accounts          []ProbeAccount
	Imported          int
	Skipped           int
	SelectedAccountID int64
}

// Probe 读取本机 Telegram Desktop 的 tdata，逐个账号联网校验，
// 需要时把有效账号写入 session 文件与数据库。
//
// 出站代理来自配置表：http_proxy_enabled=false 时直连。
func (s *Service) Probe(ctx context.Context, opts ProbeOptions) (ProbeResult, error) {
	proxy, err := config.NewStore(s.db).Proxy()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("读取代理配置: %w", err)
	}

	raw, err := probeDesktop(ctx, mtproto.DesktopProbeOptions{
		TDataPath: opts.TDataPath,
		Passcode:  passcodeBytes(opts.Passcode),
		Proxy:     proxy,
	})
	if err != nil {
		return ProbeResult{}, err
	}

	out := ProbeResult{TDataPath: raw.TDataPath}
	for _, item := range raw.Accounts {
		account := ProbeAccount{
			Index:   item.Index,
			UserID:  item.UserID,
			Devices: item.Devices,
		}
		if item.Account != nil {
			account.Name = item.Account.DisplayName()
			account.Username = item.Account.Username
			account.Phone = item.Account.Phone
		}
		if item.Err != nil {
			account.Err = item.Err
		}
		out.Accounts = append(out.Accounts, account)
	}

	if !opts.Import {
		return out, nil
	}

	// session 固定落在数据目录下，不再接受覆盖参数。
	sessionDir, err := appdir.SessionsDir()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("确定 session 目录: %w", err)
	}
	imported, err := s.ImportDesktop(sessionDir, raw)
	if err != nil {
		return ProbeResult{}, err
	}
	out.Imported, out.Skipped = imported.Imported, imported.Skipped

	// 与 CLI 一致：库里只剩一个账号时自动设为当前账号。
	if selected, err := s.AutoSelectSingle(); err == nil && selected {
		if id, ok, err := s.CurrentAccountID(); err == nil && ok {
			out.SelectedAccountID = id
		}
	}
	return out, nil
}

// passcodeBytes 只在提供了本地密码时才传，避免空密码被当成「有密码」。
func passcodeBytes(passcode string) []byte {
	if passcode == "" {
		return nil
	}
	return []byte(passcode)
}
