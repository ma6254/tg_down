package login

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/ma6254/tg_down/cmd/account/internal/store"
	"github.com/ma6254/tg_down/internal/appdir"
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
	Probe         bool          // 探测本机 Telegram Desktop（tdata）
	ProbeSession  bool          // 探测本工具自己的 session
	TData         string        // Telegram Desktop 的 tdata 目录
	TDataPasscode string        // Telegram Desktop 本地密码
	ProbeTimeout  time.Duration // tdata 探测超时
}

// newLoginOptions 返回默认值：默认走 MTProto + desktop 内置应用，
// 环境变量里的 api 凭据会覆盖内置应用。
//
// session 文件固定放在数据目录下的 sessions，不再提供 --session-dir/--session。
func newLoginOptions() *loginOptions {
	apiID, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("TG_DOWN_API_ID")))
	return &loginOptions{
		Kind:         accountKindMTProto,
		App:          mtproto.AppDesktop,
		APIID:        apiID,
		APIHash:      strings.TrimSpace(os.Getenv("TG_DOWN_API_HASH")),
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
	fs.BoolVar(&o.Probe, "probe", o.Probe, "探测本机 Telegram Desktop（tdata）中已登录的账号并导入数据库，不触发登录")
	fs.BoolVar(&o.ProbeSession, "probe-session", o.ProbeSession, "探测本工具自己保存的 session，不登录")
	fs.StringVar(&o.TData, "tdata", o.TData, "Telegram Desktop 的 tdata 目录，留空自动查找")
	fs.StringVar(&o.TDataPasscode, "tdata-passcode", o.TDataPasscode, "Telegram Desktop 的本地密码（若设置过）")
	fs.DurationVar(&o.ProbeTimeout, "probe-timeout", o.ProbeTimeout, "探测 tdata 时连接 Telegram 的超时时间")
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

// validateAPI 校验应用凭据，mtproto 与 probe 共用。
//
// 默认使用内置 desktop 应用，因此不再强制要求 api_id/api_hash；
// 一旦提供了其中一个，就必须成对提供（视为自备应用）。
// session 目录留空是合法的：执行时会解析成数据目录下的 sessions。
func (o *loginOptions) validateAPI() error {
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

// resolveApp 返回本次连接使用的应用凭据。
//
// 优先级：显式 --api-id/--api-hash > settings 里的 api_id/api_hash > 内置 desktop。
func (o *loginOptions) resolveApp() (mtproto.App, error) {
	if o.APIID > 0 && o.APIHash != "" {
		return mtproto.App{ID: o.APIID, Hash: o.APIHash}, nil
	}

	// 数据库不可用时退回内置 desktop：登录本身仍可继续，只是不读自定义凭据。
	if svc, closeFn, err := store.NewService(); err == nil {
		app, custom, appErr := svc.App()
		closeFn()
		if appErr != nil {
			return mtproto.App{}, appErr
		}
		if custom {
			return app, nil
		}
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

// sessionsDir 返回固定的 session 目录：<数据目录>/sessions。
//
// 目录跟着 TG_DOWN_HOME 走，不提供命令行覆盖；登录与探测都只认这一处。
func (o *loginOptions) sessionsDir() (string, error) {
	return appdir.SessionsDir()
}

// sessionPath 按手机号生成 session 文件路径，便于同机多账号共存。
func (o *loginOptions) sessionPath() (string, error) {
	dir, err := o.sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mtproto-"+digitsOnly(o.Phone)+".json"), nil
}

// resolveSessionPath 决定要读写的 session 文件：
// 有 --phone 按手机号推导，否则在数据目录的 sessions 里找唯一一个。
func (o *loginOptions) resolveSessionPath() (string, error) {
	dir, err := o.sessionsDir()
	if err != nil {
		return "", err
	}
	if o.Phone != "" {
		return o.sessionPath()
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读取 session 目录 %s: %w", dir, err)
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "mtproto-") && strings.HasSuffix(name, ".json") {
			found = append(found, filepath.Join(dir, name))
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("session 目录 %s 下没有 session 文件，请先执行 account login", dir)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("session 目录下有 %d 个 session 文件，请用 --phone 指定要探测哪一个", len(found))
	}
}
