package cmd

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// openDatabase 打开运行数据目录下的 SQLite，并确保表结构已建好。
//
// 默认路径为 ~/.tg_down/data.db，可用 TG_DOWN_HOME 覆盖目录。
func openDatabase() (*database.DB, error) {
	path, err := appdir.DBPath()
	if err != nil {
		return nil, err
	}
	db, err := database.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}
	if err := db.Install(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化数据库 %s: %w", path, err)
	}
	return db, nil
}

// recordAccount 把登录成功的账号登记到数据库（按 session 路径去重），返回落库后的记录。
func recordAccount(lo *loginOptions, account *mtproto.Account, sessionPath string) (*database.Account, error) {
	db, err := openDatabase()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	name := lo.Name
	if name == "" {
		name = account.DisplayName()
	}

	row, err := db.FindAccountBySessionPath(sessionPath)
	switch {
	case err == nil:
		// 已存在：更新可变字段。
	case errors.Is(err, gorm.ErrRecordNotFound):
		row = &database.Account{
			Kind:        database.AccountKindMTProto,
			SessionPath: sessionPath,
		}
	default:
		return nil, err
	}

	row.Kind = database.AccountKindMTProto
	row.Name = name
	row.Phone = account.Phone
	row.Username = account.Username
	row.State = database.AccountStateAuthed
	row.LastError = ""

	if err := db.SaveAccount(row); err != nil {
		return nil, err
	}
	return row, nil
}

// autoSelectSingleAccount 如果数据库里只有一个账号，就把它设为当前账号。
//
// 返回是否发生了自动选中（已经是当前账号时返回 false）。
func autoSelectSingleAccount() (bool, error) {
	db, err := openDatabase()
	if err != nil {
		return false, err
	}
	defer db.Close()

	count, err := db.CountAccounts()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, nil
	}

	accounts, err := db.ListAccounts()
	if err != nil {
		return false, err
	}
	if len(accounts) != 1 {
		return false, nil
	}

	store := config.NewStore(db)
	current, ok, err := store.CurrentAccountID()
	if err != nil {
		return false, err
	}
	if ok && current == accounts[0].ID {
		return false, nil
	}
	if err := store.SetCurrentAccountID(accounts[0].ID); err != nil {
		return false, err
	}
	return true, nil
}

// dataDirHint 返回数据目录的可读提示，用于登录成功后的输出。
func dataDirHint() string {
	dir, err := appdir.DataDir()
	if err != nil {
		return ""
	}
	return dir
}
