package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/config"
)

// accountSwitchCmd 按账号 ID 切换后续操作默认使用的账号。
var accountSwitchCmd = &cobra.Command{
	Use:     "switch <账号ID>",
	Aliases: []string{"use"},
	Short:   "按账号 ID 切换当前使用的账号",
	Long: `按账号 ID 切换后续命令默认使用的账号。

ID 就是 account list 输出里第一列的编号：
  tg_down account list
  tg_down account switch 3

切换前会确认该账号的 session 文件存在。不带参数时只显示当前账号。
选择结果保存在数据库的 settings.current_account_id，account list 会用 * 标出当前账号。`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAccountSwitch,
}

// runAccountSwitch 按 ID 切换账号；不带参数时显示当前账号。
func runAccountSwitch(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return printCurrentAccount()
	}

	id, err := parseAccountID(args[0])
	if err != nil {
		return err
	}

	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("账号 ID %d 不存在，可用 account list 查看", id)
		}
		return fmt.Errorf("查询账号 %d: %w", id, err)
	}

	if strings.TrimSpace(account.SessionPath) == "" {
		return fmt.Errorf("账号 #%d 没有 session 文件，无法切换（请重新登录）", account.ID)
	}
	if info, statErr := os.Stat(account.SessionPath); statErr != nil || info.IsDir() {
		return fmt.Errorf("账号 #%d 的 session 文件 %s 不存在，请重新登录", account.ID, account.SessionPath)
	}

	if err := config.NewStore(db).SetCurrentAccountID(account.ID); err != nil {
		return fmt.Errorf("保存当前账号: %w", err)
	}

	displayName := account.Name
	if displayName == "" {
		displayName = "-"
	}
	fmt.Printf("\n已切换当前账号\n  ID       %d\n  名称     %s\n  session  %s\n", account.ID, displayName, account.SessionPath)
	return nil
}

// parseAccountID 解析并校验账号 ID；只接受纯数字，避免手机号被当成 ID。
func parseAccountID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("账号 ID 不能为空（可用 account list 查看）")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("账号 ID 必须是纯数字，当前为 %q（可用 account list 查看）", raw)
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("账号 ID 必须是正整数，当前为 %q（可用 account list 查看）", raw)
	}
	return id, nil
}

// printCurrentAccount 输出当前选中的账号。
func printCurrentAccount() error {
	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	id, ok, err := config.NewStore(db).CurrentAccountID()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Println("\n当前账号：未设置（用 account switch <账号ID> 选择）")
		return nil
	}

	account, err := db.GetAccount(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("\n当前账号  #%d（数据库中已不存在，可重新执行 account switch）\n", id)
			return nil
		}
		return fmt.Errorf("查询账号 %d: %w", id, err)
	}

	displayName := account.Name
	if displayName == "" {
		displayName = "-"
	}
	fmt.Printf("\n当前账号\n  ID       %d\n  名称     %s\n  session  %s\n", account.ID, displayName, account.SessionPath)
	return nil
}
