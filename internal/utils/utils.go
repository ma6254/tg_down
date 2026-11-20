// Package utils 汇总命令层与业务层共用的小工具：
// 打开数据库、读取代理、文件读写、网络错误判断与展示格式化。
package utils

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// DefaultNetworkTimeout 是涉及网络的只读操作（探测等）的默认超时。
const DefaultNetworkTimeout = 30 * time.Second

// normalizeSessionPathsOnce 让存量 session_path 的规范化每个进程只做一次。
var normalizeSessionPathsOnce sync.Once

// OpenDatabase 打开运行数据目录下的 SQLite，并确保表结构已建好。
//
// 默认路径为 ~/.tg_down/data.db，可用 TG_DOWN_HOME 覆盖目录。
func OpenDatabase() (*database.DB, error) {
	path, err := appdir.DBPath()
	if err != nil {
		return nil, dataDirError(err)
	}
	db, err := database.Open("sqlite", path)
	if err != nil {
		return nil, sqliteOpenError(path, fmt.Errorf("打开数据库 %s: %w", path, err))
	}
	if err := db.Install(); err != nil {
		_ = db.Close()
		return nil, sqliteOpenError(path, fmt.Errorf("初始化数据库 %s: %w", path, err))
	}
	// 历史数据里的 session_path 可能是绝对路径，开库后统一改成相对数据目录。
	normalizeSessionPathsOnce.Do(func() {
		if err := db.NormalizeSessionPaths(); err != nil {
			log.Printf("规范化 session_path 失败（继续使用原值）: %v", err)
		}
	})
	return db, nil
}

// dataDirError 给「建不了数据目录」补上可操作提示。
//
// 受限终端（沙箱）或安全软件（如 Windows Defender 的受控文件夹访问）
// 都可能拒绝在用户目录下写文件；这和程序逻辑无关，换可写目录或放行即可。
func dataDirError(err error) error {
	if err == nil || !(errors.Is(err, os.ErrPermission) ||
		strings.Contains(strings.ToLower(err.Error()), "access is denied")) {
		return err
	}
	return fmt.Errorf("%w；无法写入用户目录：如果这是受限终端或沙箱，请在普通终端运行；"+
		"如果用 TG_DOWN_HOME 指定了目录，请确认该目录可写", err)
}

// sqliteOpenError 给 SQLite 的「unable to open database file」补上可操作提示。
//
// 这个错误几乎总是「数据目录里无法新建文件」：目录只读、被安全软件拦截，
// 或进程运行在受限沙箱里——SQLite 建库、建 WAL/回滚日志都要在同一目录写文件。
func sqliteOpenError(path string, err error) error {
	if err == nil || !strings.Contains(err.Error(), "unable to open database file") {
		return err
	}
	return fmt.Errorf("%w；数据目录 %s 不可写：请检查目录权限与安全软件拦截"+
		"（Windows 可查看「受控文件夹访问」），或用 TG_DOWN_HOME 指向可写目录", err, filepath.Dir(path))
}

// checkWritable 通过新建再删除一个临时文件，确认目录确实可写。
//
// Windows 上目录的「只读」属性并不可靠，权限问题往往要等 SQLite 建 WAL
// 时才暴露成难懂的 CANTOPEN，所以启动时先探一次。
func checkWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

// ResolveProxy 从配置读取实际生效的代理：
// http_proxy_enabled=false 或未配置 http_proxy 时返回空串（直连）。
func ResolveProxy() (string, error) {
	db, err := OpenDatabase()
	if err != nil {
		return "", err
	}
	defer db.Close()

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return "", fmt.Errorf("读取代理配置: %w", err)
	}
	return proxy, nil
}

// PrepareDatabase 打开数据库并完成建表、配置类型同步与默认配置写入。
//
// 服务启动时先调它再监听：并发首个请求各自 AutoMigrate 会撞上迁移竞态，
// 提前初始化也能让数据库错误在启动阶段就暴露。
func PrepareDatabase() error {
	path, err := appdir.DBPath()
	if err != nil {
		return err
	}
	// 先探一次目录可写：否则只读目录要等 SQLite 建 WAL 才报 CANTOPEN。
	if err := checkWritable(filepath.Dir(path)); err != nil {
		return fmt.Errorf("数据目录 %s 不可写: %w", filepath.Dir(path), err)
	}

	db, err := OpenDatabase()
	if err != nil {
		return err
	}
	if err := config.SyncTypes(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("同步配置类型: %w", err)
	}
	if err := config.SeedDefaults(db); err != nil {
		_ = db.Close()
		return fmt.Errorf("初始化默认配置: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("关闭数据库: %w", err)
	}
	return nil
}

// ErrRecordNotFound 暴露 gorm 的未找到错误，方便命令层判断。
var ErrRecordNotFound = gorm.ErrRecordNotFound

// IsNotFound 判断错误是否为记录不存在。
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// AccountAttrs 汇总账号的附加属性。
func AccountAttrs(account *mtproto.Account) string {
	var attrs []string
	if account.Premium {
		attrs = append(attrs, "Premium")
	}
	if account.Bot {
		attrs = append(attrs, "Bot")
	}
	return strings.Join(attrs, " / ")
}

// DescribeDevice 生成单行客户端描述。
func DescribeDevice(d mtproto.Device) string {
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
	parts = append(parts, "最近活跃 "+FormatTime(d.ActiveAt))
	return strings.Join(parts, " · ")
}

// FormatTime 格式化时间，零值显示为未知。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	return t.Format("2006-01-02 15:04")
}

// UnixOrZero 把零值时间转成 0，方便 JSON 里用 omitempty 省略。
func UnixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// RandomToken 生成 128 位随机 token 的十六进制串（32 个字符）。
//
// 用于不可猜测的一次性地址；失败时退化成纳秒时间戳，至少保证唯一。
func RandomToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// MaskPhone 把手机号中间部分打码，避免打印明文。
// Telegram 返回的号码可能不带 "+"，这里统一补上再打码。
func MaskPhone(phone string) string {
	p := phone
	if p != "" && !strings.HasPrefix(p, "+") {
		p = "+" + p
	}
	if len(p) <= 8 {
		return p
	}
	return p[:4] + "****" + p[len(p)-3:]
}

// DigitsOnly 只保留数字，用于生成文件名或比较号码。
func DigitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// OrDash 把空字符串替换为占位符。
func OrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
