package login

import (
	"fmt"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// printDesktopProbe 输出本机 Telegram Desktop 的探测结果。
func printDesktopProbe(result *mtproto.DesktopProbeResult) {
	fmt.Printf("\n  tdata     %s\n  账号数    %d\n", result.TDataPath, len(result.Accounts))

	ok := 0
	for _, item := range result.Accounts {
		fmt.Printf("\n  ── 账号 #%d ──\n", item.Index)
		if item.Err != nil {
			fmt.Printf("  探测失败  %v\n", item.Err)
			continue
		}

		account := item.Account
		fmt.Printf("  账号      %s\n", account.DisplayName())
		fmt.Printf("  账号 ID   %d\n", account.ID)
		if account.Username != "" {
			fmt.Printf("  用户名    @%s\n", account.Username)
		}
		if account.Phone != "" {
			fmt.Printf("  手机号    %s\n", maskPhone(account.Phone))
		}
		if attrs := accountAttrs(account); attrs != "" {
			fmt.Printf("  账号属性  %s\n", attrs)
		}

		fmt.Printf("\n  已登录客户端（%d）\n", len(item.Devices))
		for _, device := range item.Devices {
			mark := " "
			if device.Current {
				mark = "*"
			}
			fmt.Printf("  %s %s\n", mark, describeDevice(device))
		}
		ok++
	}

	if ok == 0 {
		fmt.Println("\n没有探测到有效的账号授权（可能已退出登录，或 tdata 需要本地密码）")
	}
}

// printProbeInfo 输出探测结果。
func printProbeInfo(info *mtproto.SessionInfo, sessionPath string) {
	fmt.Printf("\n  探测 session  %s\n", sessionPath)
	if !info.Authorized {
		fmt.Println("  登录状态      未授权（session 已失效，请重新执行 account login）")
		return
	}

	account := info.Account
	fmt.Println("  登录状态      已授权")
	fmt.Printf("  账号          %s\n", account.DisplayName())
	fmt.Printf("  账号 ID       %d\n", account.ID)
	if account.Username != "" {
		fmt.Printf("  用户名        @%s\n", account.Username)
	}
	if account.Phone != "" {
		fmt.Printf("  手机号        %s\n", maskPhone(account.Phone))
	}
	if attrs := accountAttrs(account); attrs != "" {
		fmt.Printf("  账号属性      %s\n", attrs)
	}

	fmt.Printf("\n已登录客户端（%d）\n", len(info.Devices))
	for _, device := range info.Devices {
		mark := " "
		if device.Current {
			mark = "*"
		}
		fmt.Printf("  %s %s\n", mark, describeDevice(device))
	}
	if len(info.Devices) > 0 {
		fmt.Println("\n  * 表示当前会话")
	}
}
