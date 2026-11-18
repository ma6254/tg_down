package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// 账号类型。
const (
	accountKindMTProto = "mtproto"
	accountKindBotAPI  = "botapi"
)

var (
	// botTokenPattern 匹配 Bot Token 的基本形态：<bot_id>:<secret>。
	botTokenPattern = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)
	// phonePattern 匹配 E.164 手机号。
	phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
)

// errBotAPINotImplemented 表示 Bot API 登录尚未接入。
var errBotAPINotImplemented = errors.New("Bot API 登录尚未接入：当前先支持 mtproto 用户账号")

// loginOptions 是 account login 的参数。
type loginOptions struct {
	Kind          string        // mtproto | botapi
	Name          string        // 展示名，空则自动生成
	Phone         string        // MTProto 手机号，E.164
	Token         string        // Bot API token
	Code          string        // 验证码（非交互模式）
	Password      string        // 两步验证密码
	APIID         int           // Telegram api_id
	APIHash       string        // Telegram api_hash
	App           string        // 内置应用名：desktop | custom
	SessionDir    string        // session 文件目录
	SessionPath   string        // 直接指定 session 文件，优先于目录推导
	Probe         bool          // 探测本机 Telegram Desktop（tdata）
	ProbeSession  bool          // 探测本工具自己的 session
	TData         string        // Telegram Desktop 的 tdata 目录
	TDataPasscode string        // Telegram Desktop 本地密码
	ProbeTimeout  time.Duration // tdata 探测超时
}

// newLoginOptions 返回默认值：默认走 MTProto + desktop 内置应用，
// 环境变量里的 api 凭据会覆盖内置应用。
func newLoginOptions() *loginOptions {
	apiID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("TG_DOWN_API_ID")))
	return &loginOptions{
		Kind:         accountKindMTProto,
		App:          mtproto.AppDesktop,
		APIID:        apiID,
		APIHash:      strings.TrimSpace(os.Getenv("TG_DOWN_API_HASH")),
		SessionDir:   "./sessions",
		ProbeTimeout: defaultNetworkTimeout,
	}
}

// bind 注册 login 的本地参数。
func (o *loginOptions) bind(fs *pflag.FlagSet) {
	fs.StringVar(&o.Kind, "kind", o.Kind, "账号类型：mtproto（用户账号）或 botapi（机器人）")
	fs.StringVar(&o.Name, "name", o.Name, "账号展示名，留空自动生成")
	fs.StringVar(&o.Phone, "phone", o.Phone, "MTProto 手机号，E.164 格式，例如 +8613800000000")
	fs.StringVar(&o.Token, "token", o.Token, "Bot API token，例如 123456789:AA...")
	fs.StringVar(&o.Code, "code", o.Code, "登录验证码；留空则在登录时交互输入")
	fs.StringVar(&o.Password, "password", o.Password, "两步验证密码；留空则在需要时交互输入")
	fs.IntVar(&o.APIID, "api-id", o.APIID, "自备 Telegram api_id（my.telegram.org），也可用环境变量 TG_DOWN_API_ID")
	fs.StringVar(&o.APIHash, "api-hash", o.APIHash, "自备 Telegram api_hash，也可用环境变量 TG_DOWN_API_HASH")
	fs.StringVar(&o.App, "app", o.App, "内置应用：desktop（Telegram Desktop 凭据，零配置）或 custom（配合 --api-id/--api-hash）")
	fs.StringVar(&o.SessionDir, "session-dir", o.SessionDir, "session 文件存放目录")
	fs.StringVar(&o.SessionPath, "session", o.SessionPath, "直接指定 session 文件（优先于 --phone/--session-dir 推导）")
	fs.BoolVar(&o.Probe, "probe", o.Probe, "探测本机 Telegram Desktop（tdata）中已登录的账号并导入数据库，不触发登录")
	fs.BoolVar(&o.ProbeSession, "probe-session", o.ProbeSession, "探测本工具自己保存的 session，不登录")
	fs.StringVar(&o.TData, "tdata", o.TData, "Telegram Desktop 的 tdata 目录，留空自动查找")
	fs.StringVar(&o.TDataPasscode, "tdata-passcode", o.TDataPasscode, "Telegram Desktop 的本地密码（若设置过）")
	fs.DurationVar(&o.ProbeTimeout, "probe-timeout", o.ProbeTimeout, "探测 tdata 时连接 Telegram 的超时时间")
}

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

func (o *loginOptions) validate() error {
	o.Kind = strings.ToLower(strings.TrimSpace(o.Kind))
	o.Name = strings.TrimSpace(o.Name)
	o.Phone = strings.TrimSpace(o.Phone)
	o.Token = strings.TrimSpace(o.Token)
	o.Code = strings.TrimSpace(o.Code)
	o.Password = strings.TrimSpace(o.Password)
	o.APIHash = strings.TrimSpace(o.APIHash)
	o.App = strings.ToLower(strings.TrimSpace(o.App))
	o.SessionDir = strings.TrimSpace(o.SessionDir)
	o.SessionPath = strings.TrimSpace(o.SessionPath)

	switch o.Kind {
	case accountKindMTProto:
		if o.Token != "" {
			return errors.New("--kind mtproto 不需要 --token，Bot Token 仅用于 botapi")
		}

		// 探测本机 Telegram Desktop 的授权：不需要手机号、验证码，也不需要本工具的 session。
		if o.Probe {
			if o.Code != "" || o.Password != "" {
				return errors.New("--probe 只读取 Telegram Desktop 的授权，不需要 --code / --password")
			}
			return nil
		}

		if err := o.validateAPI(); err != nil {
			return err
		}
		if o.ProbeSession {
			if o.Code != "" || o.Password != "" {
				return errors.New("--probe-session 只读取已有 session，不需要 --code / --password")
			}
			if o.Phone != "" && !phonePattern.MatchString(o.Phone) {
				return fmt.Errorf("--phone %q 不是合法的 E.164 手机号，例如 +8613800000000", o.Phone)
			}
			return nil
		}
		if o.Phone == "" {
			return errors.New("--kind mtproto 需要 --phone")
		}
		if !phonePattern.MatchString(o.Phone) {
			return fmt.Errorf("--phone %q 不是合法的 E.164 手机号，例如 +8613800000000", o.Phone)
		}
	case accountKindBotAPI:
		if o.Probe || o.ProbeSession {
			return errors.New("--probe 与 --probe-session 仅支持 mtproto 账号")
		}
		if o.Token == "" {
			return errors.New("--kind botapi 需要 --token")
		}
		if !botTokenPattern.MatchString(o.Token) {
			return errors.New("--token 形态不合法，应形如 123456789:AAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
		}
		if o.Phone != "" {
			return errors.New("--kind botapi 不需要 --phone")
		}
		if o.Code != "" || o.Password != "" {
			return errors.New("--code 与 --password 仅用于 mtproto 登录")
		}
	default:
		return fmt.Errorf("--kind 只能是 %s 或 %s，当前为 %q", accountKindMTProto, accountKindBotAPI, o.Kind)
	}
	return nil
}

// validateAPI 校验应用凭据与 session 目录，mtproto 与 probe 共用。
//
// 默认使用内置 desktop 应用，因此不再强制要求 api_id/api_hash；
// 一旦提供了其中一个，就必须成对提供（视为自备应用）。
func (o *loginOptions) validateAPI() error {
	if o.SessionDir == "" {
		return errors.New("--session-dir 不能为空")
	}
	if o.App == "" {
		o.App = mtproto.AppDesktop
	}
	if o.App != mtproto.AppDesktop && o.App != mtproto.AppCustom {
		return fmt.Errorf("--app 只能是 %s 或 %s，当前为 %q", mtproto.AppDesktop, mtproto.AppCustom, o.App)
	}

	if (o.APIID > 0) != (o.APIHash != "") {
		return errors.New("--api-id 与 --api-hash 需要同时提供")
	}
	if o.App == mtproto.AppCustom && (o.APIID <= 0 || o.APIHash == "") {
		return errors.New("--app custom 需要同时提供 --api-id 与 --api-hash：请到 https://my.telegram.org 申请")
	}
	return nil
}

// resolveApp 返回本次连接使用的应用凭据：显式 api 凭据优先，其次内置 desktop。
func (o *loginOptions) resolveApp() (mtproto.App, error) {
	if o.APIID > 0 && o.APIHash != "" {
		return mtproto.App{ID: o.APIID, Hash: o.APIHash}, nil
	}
	return mtproto.PresetApp(o.App)
}

// displayName 返回展示名，未指定时按账号类型生成。
func (o *loginOptions) displayName() string {
	if o.Name != "" {
		return o.Name
	}
	if o.Kind == accountKindBotAPI {
		return "bot:" + strings.SplitN(o.Token, ":", 2)[0]
	}
	return maskPhone(o.Phone)
}

// sessionPath 按手机号生成 session 文件路径，便于同机多账号共存。
func (o *loginOptions) sessionPath() string {
	return filepath.Join(o.SessionDir, "mtproto-"+digitsOnly(o.Phone)+".json")
}

// resolveSessionPath 决定要读写的 session 文件：
// --session 优先，其次是 --phone 推导，最后在目录里找唯一一个 session。
func (o *loginOptions) resolveSessionPath() (string, error) {
	if o.SessionPath != "" {
		return o.SessionPath, nil
	}
	if o.Phone != "" {
		return o.sessionPath(), nil
	}

	entries, err := os.ReadDir(o.SessionDir)
	if err != nil {
		return "", fmt.Errorf("读取 session 目录 %s: %w", o.SessionDir, err)
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "mtproto-") && strings.HasSuffix(name, ".json") {
			found = append(found, filepath.Join(o.SessionDir, name))
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("session 目录 %s 下没有 session 文件，请先执行 account login", o.SessionDir)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("session 目录下有 %d 个 session 文件，请用 --session 或 --phone 指定要探测哪一个", len(found))
	}
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
`, lo.displayName(), maskPhone(lo.Phone), lo.appLabel(), app.ID, sessionPath)

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

// runProbe 探测已有 session 并打印账号与已登录客户端。
func runProbe(ctx context.Context, lo *loginOptions, app mtproto.App, sessionPath, proxy string) error {
	session := mtproto.Session{App: app, SessionPath: sessionPath, Proxy: proxy}
	info, err := session.Probe(ctx)
	if err != nil {
		return err
	}
	printProbeInfo(info, sessionPath)
	return nil
}

// appLabel 返回当前应用的可读标识，便于确认是否用了自备凭据。
func (o *loginOptions) appLabel() string {
	if o.APIID > 0 && o.APIHash != "" {
		return "custom"
	}
	return o.App
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
		fmt.Println("未配置代理（config set http_proxy …），将直连 Telegram")
	}

	desktop := mtproto.Desktop{TDataPath: lo.TData, Passcode: lo.TDataPasscode, Proxy: proxy}
	result, err := desktop.Probe(probeCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("探测超时（%s）：请检查网络或代理配置（config set http_proxy …），或用 --probe-timeout 调大", timeout)
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
	for _, item := range result.Accounts {
		if item.Err != nil || item.Account == nil {
			continue
		}

		phone := strings.TrimSpace(item.Account.Phone)
		if phone == "" {
			continue
		}
		sessionPath := filepath.Join(lo.SessionDir, "mtproto-"+digitsOnly(phone)+".json")

		if _, statErr := os.Stat(sessionPath); statErr == nil {
			skipped++
		} else {
			if err := mtproto.SaveSessionFile(sessionPath, item.Session); err != nil {
				return imported, skipped, fmt.Errorf("保存 session %s: %w", sessionPath, err)
			}
			imported++
		}

		if _, err := recordAccount(lo, item.Account, sessionPath); err != nil {
			return imported, skipped, fmt.Errorf("写入账号数据库: %w", err)
		}
	}
	return imported, skipped, nil
}

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

// accountAttrs 汇总账号的附加属性。
func accountAttrs(account *mtproto.Account) string {
	var attrs []string
	if account.Premium {
		attrs = append(attrs, "Premium")
	}
	if account.Bot {
		attrs = append(attrs, "Bot")
	}
	return strings.Join(attrs, " / ")
}

// describeDevice 生成单行客户端描述。
func describeDevice(d mtproto.Device) string {
	parts := make([]string, 0, 5)
	if d.AppName != "" {
		parts = append(parts, d.AppName)
	}
	if d.Platform != "" {
		parts = append(parts, d.Platform)
	}
	if d.DeviceModel != "" {
		parts = append(parts, d.DeviceModel)
	}
	if len(parts) == 0 {
		parts = append(parts, "未知客户端")
	}
	if d.Country != "" {
		parts = append(parts, d.Country)
	}
	parts = append(parts, "最近活跃 "+formatTime(d.ActiveAt))
	return strings.Join(parts, " · ")
}

// formatTime 格式化时间，零值显示为未知。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	return t.Format("2006-01-02 15:04")
}

// maskPhone 把手机号中间部分打码，避免打印明文。
// Telegram 返回的号码可能不带 "+"，这里统一补上再打码。
func maskPhone(phone string) string {
	p := phone
	if p != "" && !strings.HasPrefix(p, "+") {
		p = "+" + p
	}
	if len(p) <= 8 {
		return p
	}
	return p[:4] + "****" + p[len(p)-3:]
}

// digitsOnly 只保留数字，用于生成文件名。
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
