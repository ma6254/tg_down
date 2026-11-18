// Package database 封装 GORM 数据层。
//
// 建表统一走 Install，避免业务代码里散落 AutoMigrate；
// 驱动使用纯 Go 的 glebarez/sqlite（底层 modernc.org/sqlite），不需要 CGO。
package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// DB 持有 GORM 句柄。
type DB struct {
	*gorm.DB
	driver string
}

// Open 建立数据库连接，目前只支持 sqlite。
func Open(driver, dsn string) (*DB, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	if driver == "" {
		driver = "sqlite"
	}

	var dialector gorm.Dialector
	switch driver {
	case "sqlite":
		if err := ensureDir(dsn); err != nil {
			return nil, err
		}
		dialector = sqlite.Open(dsn)
	default:
		return nil, fmt.Errorf("暂不支持的数据库驱动 %q", driver)
	}

	logger := gormlogger.New(
		log.New(os.Stderr, "", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	gdb, err := gorm.Open(dialector, &gorm.Config{Logger: logger})
	if err != nil {
		return nil, err
	}
	return &DB{DB: gdb, driver: driver}, nil
}

// Install 首次建表；新增实体必须在这里登记。
func (d *DB) Install() error {
	return d.AutoMigrate(&Account{}, &Setting{})
}

// Close 关闭底层连接。
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Driver 返回当前驱动名。
func (d *DB) Driver() string { return d.driver }

// ensureDir 为 sqlite 文件路径自动创建父目录。
func ensureDir(dsn string) error {
	if strings.Contains(dsn, ":memory:") {
		return nil
	}

	path := dsn
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
