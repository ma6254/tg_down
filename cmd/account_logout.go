package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
)

// logoutOptions 是 account logout 的参数。
type logoutOptions struct {
	All        bool   // 删除全部账号
	Yes        bool   // 跳过交互确认
	TargetID   int64  // 按 ID 删除时的目标
	TargetName string // 按展示名/手机号删除时的目标
}

var logoutOpts = &logoutOptions{}

// bind 注册 logout 的本地参数。
func (o *logoutOptions) bind(fs *pflag.FlagSet) {
	fs.BoolVar(&o.All, "all", o.All, "删除全部账号")
	fs.BoolVar(&o.Yes, "yes", o.Yes, "跳过交互确认")
}

// validate 校验目标账号，并把参数归一化成 ID 或名称。
func (o *logoutOptions) validate(args []string) error {
	o.TargetID = 0
	o.TargetName = ""

	if o.All {
		if len(args) > 0 {
			return errors.New("--all 不能同时指定账号 ID 或名称")
		}
		return nil
	}

	if len(args) == 0 {
		return errors.New("请指定账号 ID 或名称，或使用 --all 删除全部账号")
	}

	target := strings.TrimSpace(args[0])
	if target == "" {
		return errors.New("账号 ID 或名称不能为空")
	}

	if id, err := strconv.ParseInt(target, 10, 64); err == nil {
		if id <= 0 {
			return fmt.Errorf("账号 ID 必须为正整数，当前为 %q", target)
		}
		o.TargetID = id
		return nil
	}
	o.TargetName = target
	return nil
}

// summary 返回目标账号的可读描述。
func (o *logoutOptions) summary() string {
	if o.All {
		return "全部账号"
	}
	if o.TargetID > 0 {
		return fmt.Sprintf("ID %d", o.TargetID)
	}
	return fmt.Sprintf("名称 %q", o.TargetName)
}

// accountLogoutCmd 退出登录并删除本地账号。
var accountLogoutCmd = &cobra.Command{
	Use:     "logout <账号ID|名称>",
	Aliases: []string{"remove", "delete"},
	Short:   "退出登录并删除本地账号",
	Long: `退出登录并删除本地保存的账号。

会同时删除数据库记录与对应的 session 文件，不可恢复；已下载的文件不受影响。

  tg_down account logout 1          按 ID
  tg_down account logout my_bot     按展示名或手机号
  tg_down account logout --all      删除全部账号`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAccountLogout,
}

func init() {
	logoutOpts.bind(accountLogoutCmd.Flags())
}

// runAccountLogout 删除选中的账号及其 session 文件。
func runAccountLogout(_ *cobra.Command, args []string) error {
	if err := logoutOpts.validate(args); err != nil {
		return err
	}

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	targets, err := findLogoutTargets(db)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		fmt.Println("没有找到要删除的账号")
		return nil
	}

	if !logoutOpts.Yes {
		printLogoutTargets(targets)
		fmt.Print("\n确认删除以上账号？输入 yes 继续: ")
		if !confirmStdin() {
			fmt.Println("已取消")
			return nil
		}
	}

	removedFiles := 0
	for _, account := range targets {
		if err := db.DeleteAccount(account.ID); err != nil {
			return fmt.Errorf("删除账号 %d: %w", account.ID, err)
		}
		if removeSessionFile(account.SessionPath) {
			removedFiles++
		}
	}

	clearCurrentIfDeleted(db, targets)
	fmt.Printf("\n已删除 %d 个账号", len(targets))
	if removedFiles > 0 {
		fmt.Printf("，清理 %d 个 session 文件", removedFiles)
	}
	fmt.Println()
	return nil
}

// findLogoutTargets 根据参数解析要删除的账号。
func findLogoutTargets(db *database.DB) ([]database.Account, error) {
	if logoutOpts.All {
		return db.ListAccounts()
	}
	if logoutOpts.TargetID > 0 {
		account, err := db.GetAccount(logoutOpts.TargetID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("账号 ID %d 不存在", logoutOpts.TargetID)
			}
			return nil, err
		}
		return []database.Account{*account}, nil
	}

	accounts, err := db.ListAccounts()
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(logoutOpts.TargetName)
	for _, account := range accounts {
		if account.Name == key || account.Username == key || account.Phone == key {
			return []database.Account{account}, nil
		}
		if account.Phone != "" && digitsOnly(account.Phone) == digitsOnly(key) {
			return []database.Account{account}, nil
		}
	}
	return nil, fmt.Errorf("没有找到匹配 %q 的账号", key)
}

// printLogoutTargets 打印待删除账号。
func printLogoutTargets(accounts []database.Account) {
	fmt.Println("\n待删除账号：")
	for _, account := range accounts {
		name := account.Name
		if name == "" {
			name = "-"
		}
		fmt.Printf("  #%d  %s  %s  %s\n", account.ID, account.Kind, name, account.Phone)
	}
}

// confirmStdin 读取一行确认输入。
func confirmStdin() bool {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// removeSessionFile 删除 session 文件，返回是否真的删除了文件。
func removeSessionFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return os.Remove(path) == nil
}

// clearCurrentIfDeleted 如果当前账号被删除，清空 settings.current_account_id。
func clearCurrentIfDeleted(db *database.DB, deleted []database.Account) {
	store := config.NewStore(db)
	currentID, ok, err := store.CurrentAccountID()
	if err != nil || !ok {
		return
	}
	for _, account := range deleted {
		if account.ID == currentID {
			_ = store.ClearCurrentAccountID()
			return
		}
	}
}
