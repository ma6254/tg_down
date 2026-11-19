// Package utils 汇总命令层与业务层共用的小工具：
// 打开数据库、读取代理、展示格式化。
package utils

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/appdir"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// DefaultNetworkTimeout 是涉及网络的只读操作（探测等）的默认超时。
const DefaultNetworkTimeout = 30 * time.Second

// OpenDatabase 打开运行数据目录下的 SQLite，并确保表结构已建好。
//
// 默认路径为 ~/.tg_down/data.db，可用 TG_DOWN_HOME 覆盖目录。
func OpenDatabase() (*database.DB, error) {
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

// ResolveProxy 从配置读取 http_proxy，未配置时返回空串（直连）。
func ResolveProxy() (string, error) {
	db, err := OpenDatabase()
	if err != nil {
		return "", err
	}
	defer db.Close()

	value, ok, err := config.NewStore(db).Get(config.KeyHTTPProxy)
	if err != nil {
		return "", fmt.Errorf("读取 %s: %w", config.KeyHTTPProxy, err)
	}
	if !ok {
		return "", nil
	}
	return value, nil
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
