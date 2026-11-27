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
	"sync"
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

	// SQLite 只允许一个写者；把连接池收紧到 1 并设置 busy_timeout，
	// 避免并发请求在写锁上直接失败。WAL 让读写可以并行，尽力开启，
	// 网络盘等不支持 WAL 的场景保留默认日志模式即可。
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)
	if err := gdb.Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
		return nil, fmt.Errorf("设置 SQLite busy_timeout: %w", err)
	}
	if !isMemoryDSN(dsn) {
		var mode string
		if err := gdb.Raw("PRAGMA journal_mode = WAL").Scan(&mode).Error; err != nil {
			log.Printf("设置 SQLite WAL 失败（继续使用默认日志模式）: %v", err)
		}
	}

	return &DB{DB: gdb, driver: driver}, nil
}

// installMu 串行化本进程内的 AutoMigrate。
//
// GORM 的 AutoMigrate 是「先检查再创建」，并发首次打开同一个库时
// 两个请求可能同时创建索引，后一个收到 "index ... already exists"。
// 同一进程内串行化即可；跨进程（服务与 CLI 同时启动）由 Install 的
// 重试兜底。
var installMu sync.Mutex

// Install 首次建表；新增实体必须在这里登记。
func (d *DB) Install() error {
	installMu.Lock()
	defer installMu.Unlock()

	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = d.AutoMigrate(&Account{}, &Setting{}, &DownloadTask{}, &UIDraft{}, &DownloadInput{}, &MessageCache{}, &PeerCache{}, &ThreadCache{}); err == nil {
			// 缩略图已改存文件缓存，旧列的 BLOB 不再使用，启动时清掉。
			if err = d.dropLegacyThumbnailColumn(); err == nil {
				return nil
			}
		}
		if !retryableMigrationError(err) {
			return err
		}
		log.Printf("数据库迁移竞态，重试 %d/3: %v", attempt+1, err)
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	return err
}

// retryableMigrationError 判断迁移失败是否可能由并发建表/建索引引起。
func retryableMigrationError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"already exists",
		"database is locked",
		"database table is locked",
		"sqlite_busy",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// isMemoryDSN 判断是否为 SQLite 内存库（内存库不支持 WAL）。
func isMemoryDSN(dsn string) bool {
	return strings.Contains(dsn, ":memory:") || strings.Contains(dsn, "mode=memory")
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
