package serve

import (
	"context"
	"fmt"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// probeSessionDir 是探测导入时保存 session 的目录，与 CLI 的 --session-dir 默认值一致。
const probeSessionDir = "./sessions"

// probeDesktop 是 tdata 探测实现；测试里替换成假数据，避免真的连接 Telegram。
var probeDesktop = mtproto.ProbeDesktop

// probeLogin 把 CLI 的 `account login --probe` 搬到 HTTP：
// 读取本机 Telegram Desktop 的 tdata，逐个账号连接 Telegram 校验，
// 再把有效账号写入 sessions/ 与数据库（req.Import 为 false 时只探测不落库）。
func probeLogin(ctx context.Context, req server.ProbeRequest) (server.ProbeResponse, error) {
	db, err := utils.OpenDatabase()
	if err != nil {
		return server.ProbeResponse{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	// 出站代理来自配置表：http_proxy_enabled=false 时直连。
	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return server.ProbeResponse{}, fmt.Errorf("读取代理配置: %w", err)
	}

	result, err := probeDesktop(ctx, mtproto.DesktopProbeOptions{
		TDataPath: req.TDataPath,
		Passcode:  passcodeBytes(req.TDataPasscode),
		Proxy:     proxy,
	})
	if err != nil {
		return server.ProbeResponse{}, err
	}

	resp := server.ProbeResponse{TDataPath: result.TDataPath}
	for _, item := range result.Accounts {
		account := server.ProbeAccount{
			Index:   item.Index,
			UserID:  item.UserID,
			Devices: probeDevices(item.Devices),
		}
		if item.Account != nil {
			account.Name = item.Account.DisplayName()
			account.Username = item.Account.Username
			account.Phone = item.Account.Phone
		}
		if item.Err != nil {
			account.Error = item.Err.Error()
		}
		resp.Accounts = append(resp.Accounts, account)
	}

	if !probeImportEnabled(req) {
		return resp, nil
	}

	svc := accountsvc.New(db)
	imported, err := svc.ImportDesktop(probeSessionDir, result)
	if err != nil {
		return server.ProbeResponse{}, err
	}
	resp.Imported, resp.Skipped = imported.Imported, imported.Skipped

	// 与 CLI 一致：库里只剩一个账号时自动设为当前账号。
	if selected, err := svc.AutoSelectSingle(); err == nil && selected {
		if id, ok, err := svc.CurrentAccountID(); err == nil && ok {
			resp.SelectedAccountID = id
		}
	}
	return resp, nil
}

// probeImportEnabled 解析 import 开关，缺省为 true（与 CLI 行为一致）。
func probeImportEnabled(req server.ProbeRequest) bool {
	return req.Import == nil || *req.Import
}

// passcodeBytes 只在提供了本地密码时才传，避免空密码被当成「有密码」。
func passcodeBytes(passcode string) []byte {
	if passcode == "" {
		return nil
	}
	return []byte(passcode)
}

// probeDevices 把 mtproto 的客户端信息转换成 API 响应结构。
func probeDevices(devices []mtproto.Device) []server.ProbeDevice {
	if len(devices) == 0 {
		return nil
	}
	out := make([]server.ProbeDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, server.ProbeDevice{
			Current:       d.Current,
			AppName:       d.AppName,
			AppVersion:    d.AppVersion,
			Platform:      d.Platform,
			DeviceModel:   d.DeviceModel,
			SystemVersion: d.SystemVersion,
			Country:       d.Country,
			CreatedAt:     utils.UnixOrZero(d.CreatedAt),
			ActiveAt:      utils.UnixOrZero(d.ActiveAt),
		})
	}
	return out
}
