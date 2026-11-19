package accountinfo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// infoOptions 是 account info 的参数。
type infoOptions struct {
	Live bool // 联网查询生日、简介等资料（不落库）
}

var infoOpts = &infoOptions{}

func (o *infoOptions) bind(fs *pflag.FlagSet) {
	fs.BoolVar(&o.Live, "live", o.Live, "联网查询生日、简介等资料（只查询，不写入数据库）")
}

// accountInfoCmd 查看账号的本地信息。
var accountInfoCmd = &cobra.Command{
	Use:     "info [账号ID]",
	Aliases: []string{"show"},
	Short:   "查看账号信息",
	Long: `查看账号的本地信息。

不带参数时查看当前账号（settings.current_account_id），也可以指定账号 ID：
  tg_down account info
  tg_down account info 3
  tg_down account info --live      # 额外联网查询最新资料与已登录设备

数据来自本地数据库，默认不联网；--live 会临时连一次 Telegram 查询账号资料
（服务端名称、生日、简介）与客户端列表，只展示、不写入数据库。`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAccountInfo,
}

func init() {
	infoOpts.bind(accountInfoCmd.Flags())
}

// runAccountInfo 读取账号并打印。
func runAccountInfo(_ *cobra.Command, args []string) error {
	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	id, err := resolveInfoAccountID(db, args)
	if err != nil {
		return err
	}

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("账号 ID %d 不存在，可用 account list 查看", id)
		}
		return fmt.Errorf("查询账号 %d: %w", id, err)
	}

	isCurrent := false
	if currentID, ok, curErr := config.NewStore(db).CurrentAccountID(); curErr == nil && ok {
		isCurrent = currentID == account.ID
	}

	// --live 时先联网取资料，好让「会员」行与下面的在线资料一起展示；
	// 取不到就退回离线视图，不影响本地信息。
	var live *mtproto.LiveInfo
	if infoOpts.Live {
		fmt.Println("\n正在联网查询资料（不落库）…")
		ctx, cancel := context.WithTimeout(context.Background(), defaultNetworkTimeout)
		defer cancel()

		live, err = fetchLiveInfo(ctx, account)
		if err != nil {
			fmt.Fprintf(os.Stderr, "在线资料：%v\n", err)
			live = nil
		}
	}

	printAccountInfo(os.Stdout, account, isCurrent, live)
	if live != nil {
		printLiveInfo(os.Stdout, live)
	}
	return nil
}

// fetchLiveInfo 联网查询账号资料与已登录设备（不落库）。
func fetchLiveInfo(ctx context.Context, account *database.Account) (*mtproto.LiveInfo, error) {
	if account.SessionPath == "" {
		return nil, errors.New("该账号没有 session 文件，无法联网查询")
	}

	app, err := mtproto.PresetApp(mtproto.AppDesktop)
	if err != nil {
		return nil, err
	}
	proxy, err := resolveProxy()
	if err != nil {
		return nil, err
	}

	session := mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}
	return session.Inspect(ctx)
}

// printLiveInfo 输出在线资料与已登录客户端。
func printLiveInfo(w io.Writer, info *mtproto.LiveInfo) {
	fmt.Fprintln(w, "\n在线资料")

	if account := info.Account; account != nil {
		fmt.Fprintf(w, "  %-8s  %s\n", "账号", account.DisplayName())
		if account.Username != "" {
			fmt.Fprintf(w, "  %-8s  @%s\n", "用户名", account.Username)
		}
		if account.Phone != "" {
			fmt.Fprintf(w, "  %-8s  %s\n", "手机号", maskPhone(account.Phone))
		}
		if account.Bot {
			fmt.Fprintf(w, "  %-8s  %s\n", "账号属性", "Bot")
		}
	}

	birthday := "未公开"
	if profile := info.Profile; profile != nil && profile.HasBirthday {
		if profile.BirthYear > 0 {
			birthday = fmt.Sprintf("%04d-%02d-%02d", profile.BirthYear, profile.BirthMonth, profile.BirthDay)
		} else {
			birthday = fmt.Sprintf("%d 月 %d 日（未填年份）", profile.BirthMonth, profile.BirthDay)
		}
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "生日", birthday)

	about := "-"
	if profile := info.Profile; profile != nil && profile.About != "" {
		about = profile.About
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "简介", about)

	fmt.Fprintf(w, "\n已登录客户端（%d）\n", len(info.Devices))
	for _, device := range info.Devices {
		mark := " "
		if device.Current {
			mark = "*"
		}
		fmt.Fprintf(w, "  %s %s\n", mark, describeDevice(device))
	}
	if len(info.Devices) > 0 {
		fmt.Fprintln(w, "\n  * 表示当前会话")
	}
}

// resolveInfoAccountID 决定要查看的账号：命令行优先，其次当前账号。
func resolveInfoAccountID(db *database.DB, args []string) (int64, error) {
	if len(args) > 0 {
		return parseAccountID(args[0])
	}

	id, ok, err := config.NewStore(db).CurrentAccountID()
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("未指定账号 ID，且没有当前账号（先 account switch <ID>，或直接用 account info <ID>）")
	}
	return id, nil
}

// printAccountInfo 输出账号信息表；live 非空时用其中的会员状态。
func printAccountInfo(w io.Writer, account *database.Account, current bool, live *mtproto.LiveInfo) {
	title := "账号信息"
	if current {
		title += "（当前账号）"
	}
	fmt.Fprintf(w, "\n%s\n", title)
	fmt.Fprintf(w, "  %-8s  %d\n", "ID", account.ID)
	fmt.Fprintf(w, "  %-8s  %s\n", "类型", orDash(account.Kind))
	fmt.Fprintf(w, "  %-8s  %s\n", "名称", orDash(account.Name))

	username := "-"
	if account.Username != "" {
		username = "@" + account.Username
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "用户名", username)

	phone := "-"
	if account.Phone != "" {
		phone = maskPhone(account.Phone)
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "手机号", phone)
	fmt.Fprintf(w, "  %-8s  %s\n", "状态", orDash(account.State))

	// 会员状态不落库：离线时未知，--live 时取服务端 user.premium。
	member := "未知（加 --live 查询）"
	if live != nil && live.Account != nil {
		if live.Account.Premium {
			member = "是"
		} else {
			member = "否"
		}
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "会员", member)

	session := account.SessionPath
	if session == "" {
		session = "-"
	}
	fmt.Fprintf(w, "  %-8s  %s\n", "session", session)
	fmt.Fprintf(w, "  %-8s  %s\n", "创建时间", formatTime(account.CreatedAt))
	if !account.UpdatedAt.IsZero() {
		fmt.Fprintf(w, "  %-8s  %s\n", "更新时间", formatTime(account.UpdatedAt))
	}
	if account.LastError != "" {
		fmt.Fprintf(w, "  %-8s  %s\n", "最近错误", account.LastError)
	}
}
