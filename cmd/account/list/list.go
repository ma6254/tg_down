package accountlist

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
)

// accountListCmd 列出数据库中的账号。
var accountListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "列出本地账号",
	Long: `列出本地保存的账号（别名 ls）。

账号来自数据库（~/.tg_down/data.db 的 accounts 表），当前账号用 * 标出。
需要查看本机 Telegram Desktop 的账号时，用 account login --probe。`,
	Args: cobra.NoArgs,
	RunE: runAccountList,
}

// runAccountList 输出数据库中的账号。
func runAccountList(_ *cobra.Command, _ []string) error {
	db, err := openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	accounts, err := db.ListAccounts()
	if err != nil {
		return fmt.Errorf("读取账号列表: %w", err)
	}

	currentID, _, err := config.NewStore(db).CurrentAccountID()
	if err != nil {
		return err
	}
	printAccounts(os.Stdout, accounts, currentID)
	return nil
}

// printAccounts 输出数据库中的账号表，当前账号用 * 标出。
func printAccounts(w io.Writer, accounts []database.Account, currentID int64) {
	if len(accounts) == 0 {
		fmt.Fprintln(w, "\n本地账号\n  无（执行 account login 后出现）")
		return
	}

	fmt.Fprintf(w, "\n本地账号（%d）\n", len(accounts))
	fmt.Fprintf(w, "  %-2s %-4s %-8s %-14s %-12s %-8s %s\n", "", "ID", "类型", "名称", "手机号", "状态", "session")
	for _, account := range accounts {
		mark := " "
		if currentID > 0 && account.ID == currentID {
			mark = "*"
		}
		name := account.Name
		if name == "" {
			name = "-"
		}
		phone := "-"
		if account.Phone != "" {
			phone = maskPhone(account.Phone)
		}
		fmt.Fprintf(w, "  %-2s %-4d %-8s %-14s %-12s %-8s %s\n",
			mark, account.ID, account.Kind, name, phone, account.State, filepath.Base(account.SessionPath))
	}
	fmt.Fprintln(w, "\n  * 表示当前账号（account switch 切换）")
}
