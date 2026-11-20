package login

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

var accountLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "登录并保存一个 Telegram 账号",
	Long: `登录并保存一个 Telegram 账号。

MTProto 用户账号（默认，使用内置 desktop 应用，零配置）：
  tg_down account login --phone +8613800000000
  流程：发送验证码 → 输入验证码 →（如开启两步验证）输入密码 → 保存 session

探测本机已安装的 Telegram Desktop（读取 tdata 授权，不登录、不发码）：
  tg_down account login --probe
  tg_down account login --probe --tdata "C:\Users\me\AppData\Roaming\Telegram Desktop\tdata"
  tg_down account login --probe --tdata-passcode 本地密码

探测本工具自己保存的 session（不登录、不发码）：
  tg_down account login --probe-session
  tg_down account login --probe-session --phone +8613800000000

使用自己申请的 api 凭据（配额独立，推荐长期使用）：
  tg_down account login --app custom --api-id 123456 --api-hash abcdef... --phone +86138...

Bot API 机器人（尚未接入）：
  tg_down account login --kind botapi --token 123456789:AA...

内置 desktop 使用的是 Telegram Desktop 的公开应用凭据（2040），零配置但共享配额；
需要独立配额时到 https://my.telegram.org 申请自己的应用，再用 --app custom 指定。`,
	Args: cobra.NoArgs,
	RunE: runAccountLogin,
}

var loginOpts = newLoginOptions()

func init() {
	loginOpts.bind(accountLoginCmd.Flags())
}

// runAccountLogin 执行登录或探测。
func runAccountLogin(_ *cobra.Command, _ []string) error {
	lo := loginOpts
	if err := lo.validate(); err != nil {
		return err
	}

	// 需要等待用户输入验证码或网络往返，只在 Ctrl+C 时中断。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 出站代理来自配置表 http_proxy，未配置时直连。
	proxy, err := resolveProxy()
	if err != nil {
		return err
	}

	// 探测本机 Telegram Desktop：不依赖本工具的 session 与 api 凭据。
	if lo.Probe {
		return runDesktopProbe(ctx, lo, proxy)
	}

	sessionPath, err := lo.resolveSessionPath()
	if err != nil {
		return err
	}
	app, err := lo.resolveApp()
	if err != nil {
		return err
	}

	if lo.ProbeSession {
		return runProbe(ctx, lo, app, sessionPath, proxy)
	}
	if lo.Kind == accountKindBotAPI {
		return errBotAPINotImplemented
	}

	fmt.Printf(`
  账号类型  mtproto
  展示名    %s
  手机号    %s
  应用      %s（api_id %d）
  session   %s
`, lo.displayName(), maskPhone(lo.Phone), appLabel(app), app.ID, sessionPath)

	// 出站代理已在前面解析：配置表 http_proxy。
	session := mtproto.Session{App: app, SessionPath: sessionPath, Proxy: proxy}
	account, err := session.Login(ctx, lo.Phone, lo.Code, lo.Password)
	if err != nil {
		return err
	}

	fmt.Printf(`
登录成功
  账号      %s
  账号 ID   %d
`, account.DisplayName(), account.ID)
	if account.Username != "" {
		fmt.Printf("  用户名    @%s\n", account.Username)
	}
	if account.Phone != "" {
		fmt.Printf("  手机号    %s\n", maskPhone(account.Phone))
	}
	fmt.Println("\nsession 已保存，该文件等同于账号凭证，请勿泄露或提交到仓库。")

	saved, err := recordAccount(lo, account, sessionPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告：写入账号数据库失败: %v\n", err)
		return nil
	}
	if dir := dataDirHint(); dir != "" {
		fmt.Printf("账号已登记到 %s\n", dir)
	}

	if selected, err := autoSelectSingleAccount(); err != nil {
		fmt.Fprintf(os.Stderr, "警告：自动选中账号失败: %v\n", err)
	} else if selected {
		fmt.Printf("已自动选中账号 #%d（当前唯一账号）\n", saved.ID)
	}
	return nil
}

// appLabel 返回当前应用的可读标识，便于确认是否用了自备凭据。
func appLabel(app mtproto.App) string {
	desktop, err := mtproto.PresetApp(mtproto.AppDesktop)
	if err == nil && app.ID == desktop.ID {
		return mtproto.AppDesktop
	}
	return mtproto.AppCustom
}
