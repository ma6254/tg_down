// Package config 管理 tg_down 的持久化系统配置。
//
// 配置存储在 SQLite 的 settings 表中，键、校验规则与展示脱敏都收敛在这里，
// 命令层只负责读写与输出，不再各自维护一份键表。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/ma6254/tg_down/internal/database"
)

// 配置项键名。
const (
	// KeyHTTPProxy 是出站代理。
	KeyHTTPProxy = "http_proxy"
	// KeyCurrentAccountID 是当前选中的账号 ID，由 account switch 维护。
	KeyCurrentAccountID = "current_account_id"
)

// Item 描述一个配置项。
type Item struct {
	Key         string
	Description string
	Validate    func(string) error
}

// items 是全部已知配置项，新增配置必须在这里登记。
var items = []Item{
	{
		Key:         KeyHTTPProxy,
		Description: "出站代理，支持 http/https/socks5/socks5h",
		Validate:    ValidateProxy,
	},
	{
		Key:         KeyCurrentAccountID,
		Description: "当前选中的账号 ID（由 account switch 维护）",
		Validate:    ValidateAccountID,
	},
}

// Items 返回全部配置项定义（副本，避免调用方修改）。
func Items() []Item {
	out := make([]Item, len(items))
	copy(out, items)
	return out
}

// Keys 返回全部配置项键名。
func Keys() []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

// Lookup 按 key 查找配置项。
func Lookup(key string) (Item, bool) {
	for _, item := range items {
		if item.Key == key {
			return item, true
		}
	}
	return Item{}, false
}

// Settings 是当前配置的快照。
type Settings struct {
	HTTPProxy string
}

// Load 读取全部已知配置，缺失项留空。
func Load(db *database.DB) (Settings, error) {
	var settings Settings
	value, _, err := NewStore(db).Get(KeyHTTPProxy)
	if err != nil {
		return settings, err
	}
	settings.HTTPProxy = value
	return settings, nil
}

// Entry 是一条配置记录。
type Entry struct {
	Key   string
	Value string
	Known bool // 是否为已知配置项
}

// Store 提供配置的读写能力。
type Store struct {
	db *database.DB
}

// NewStore 创建配置存储。
func NewStore(db *database.DB) *Store {
	return &Store{db: db}
}

// Get 读取单个配置项；不存在时 ok 为 false。
func (s *Store) Get(key string) (value string, ok bool, err error) {
	if _, exists := Lookup(key); !exists {
		return "", false, unknownKeyError(key)
	}

	raw, err := s.db.GetSetting(key)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(raw), true, nil
}

// Set 写入配置项，写入前按配置项规则校验。
func (s *Store) Set(key, value string) error {
	item, exists := Lookup(key)
	if !exists {
		return unknownKeyError(key)
	}
	value = strings.TrimSpace(value)
	if item.Validate != nil {
		if err := item.Validate(value); err != nil {
			return err
		}
	}
	return s.db.SetSetting(key, value)
}

// Unset 删除配置项，不存在时返回 nil。
func (s *Store) Unset(key string) error {
	if _, exists := Lookup(key); !exists {
		return unknownKeyError(key)
	}
	return s.db.DeleteSetting(key)
}

// List 返回全部配置记录，未登记的键也会列出来并标记 Known=false。
func (s *Store) List() ([]Entry, error) {
	rows, err := s.db.ListSettings()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		_, known := Lookup(row.Key)
		entries = append(entries, Entry{
			Key:   row.Key,
			Value: strings.TrimSpace(row.Value),
			Known: known,
		})
	}
	return entries, nil
}

// unknownKeyError 生成统一的未知配置项错误。
func unknownKeyError(key string) error {
	return fmt.Errorf("未知配置项 %q，可用：%s", key, strings.Join(Keys(), ", "))
}

// ValidateProxy 校验代理地址。
func ValidateProxy(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("http_proxy 不能为空，如需关闭请用 config unset http_proxy")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("代理地址 %q 不是合法 URL: %w", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h", "http", "https":
	default:
		return fmt.Errorf("代理仅支持 socks5/socks5h/http/https，当前为 %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("代理地址 %q 缺少主机与端口", raw)
	}
	return nil
}

// ValidateAccountID 校验账号 ID：必须是正整数。
func ValidateAccountID(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("current_account_id 不能为空")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fmt.Errorf("账号 ID 必须是纯数字，当前为 %q", raw)
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("账号 ID 必须是正整数，当前为 %q", raw)
	}
	return nil
}

// CurrentAccountID 读取当前选中的账号 ID；未设置时 ok 为 false。
func (s *Store) CurrentAccountID() (id int64, ok bool, err error) {
	value, ok, err := s.Get(KeyCurrentAccountID)
	if err != nil || !ok {
		return 0, false, err
	}
	id, err = strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, false, fmt.Errorf("配置项 %s 的值 %q 不是合法账号 ID", KeyCurrentAccountID, value)
	}
	return id, true, nil
}

// SetCurrentAccountID 记录当前选中的账号 ID。
func (s *Store) SetCurrentAccountID(id int64) error {
	if id <= 0 {
		return fmt.Errorf("账号 ID 必须为正整数，当前为 %d", id)
	}
	return s.Set(KeyCurrentAccountID, strconv.FormatInt(id, 10))
}

// ClearCurrentAccountID 清除当前选中的账号。
func (s *Store) ClearCurrentAccountID() error {
	return s.Unset(KeyCurrentAccountID)
}

// Mask 返回用于展示的配置值：代理类配置会隐藏密码。
func Mask(key, value string) string {
	if key != KeyHTTPProxy {
		return value
	}
	u, err := url.Parse(value)
	if err != nil || u.User == nil {
		return value
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
