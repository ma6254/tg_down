package login

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ma6254/tg_down/cmd/account/internal/store"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

func runProbe(ctx context.Context, lo *loginOptions, app mtproto.App, sessionPath, proxy string) error {
	session := mtproto.Session{App: app, SessionPath: sessionPath, Proxy: proxy}
	info, err := session.Probe(ctx)
	if err != nil {
		return err
	}
	printProbeInfo(info, sessionPath)
	return nil
}

// runDesktopProbe 读取本机 Telegram Desktop 的 tdata，探测账号并导入数据库。
func runDesktopProbe(ctx context.Context, lo *loginOptions, proxy string) error {
	timeout := lo.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultNetworkTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Printf("正在读取 tdata 并连接 Telegram（超时 %s）…\n", timeout)
	if proxy != "" {
		fmt.Printf("使用代理 %s\n", config.Mask(config.KeyHTTPProxy, proxy))
	} else {
		fmt.Println("未启用 HTTP 代理或未配置 http_proxy（config set http_proxy … / http_proxy_enabled true），将直连 Telegram")
	}

	desktop := mtproto.Desktop{TDataPath: lo.TData, Passcode: lo.TDataPasscode, Proxy: proxy}
	result, err := desktop.Probe(probeCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("探测超时（%s）：请检查网络或代理配置（config set http_proxy …，并确认 http_proxy_enabled 为 true），或用 --probe-timeout 调大", timeout)
		}
		return err
	}
	printDesktopProbe(result)

	imported, skipped, err := importDesktopAccounts(lo, result)
	if err != nil {
		return err
	}
	if imported == 0 && skipped == 0 {
		fmt.Println("\n没有可导入的账号")
		return nil
	}

	fmt.Printf("\n已导入 %d 个账号到数据库", imported)
	if skipped > 0 {
		fmt.Printf("（%d 个 session 文件已存在，保留原文件）", skipped)
	}
	if selected, selErr := autoSelectSingleAccount(); selErr != nil {
		fmt.Fprintf(os.Stderr, "\n警告：自动选中账号失败: %v", selErr)
	} else if selected {
		fmt.Print("\n已自动选中该账号（当前唯一账号）")
	}
	if dir := dataDirHint(); dir != "" {
		fmt.Printf("\n数据目录  %s", dir)
	}
	fmt.Println()
	return nil
}

// importDesktopAccounts 把探测成功的账号写入 session 文件与数据库。
//
// session 文件已存在时保留原文件，避免覆盖本工具已登录的授权；账号记录仍会更新。
func importDesktopAccounts(lo *loginOptions, result *mtproto.DesktopProbeResult) (imported, skipped int, err error) {
	svc, closeFn, err := store.NewService()
	if err != nil {
		return 0, 0, err
	}
	defer closeFn()

	out, err := svc.ImportDesktop(lo.SessionDir, result)
	return out.Imported, out.Skipped, err
}
