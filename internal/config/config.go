// Package config 管理 tg_down 的持久化系统配置。
//
// 配置存储在 SQLite 的 settings 表中，键、校验规则与展示脱敏都收敛在这里，
// 命令层只负责读写与输出，不再各自维护一份键表。
package config

import (
	"encoding/hex"
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
	// KeyHTTPProxyEnabled 控制是否启用 http_proxy；未配置时视为 true。
	KeyHTTPProxyEnabled = "http_proxy_enabled"
	// KeyCurrentAccountID 是当前选中的账号 ID，由 account switch 维护。
	KeyCurrentAccountID = "current_account_id"
	// KeyAPIID 是自定义 Telegram 应用的 api_id（my.telegram.org 申请）。
	KeyAPIID = "api_id"
	// KeyAPIHash 是自定义 Telegram 应用的 api_hash，与 api_id 配套。
	KeyAPIHash = "api_hash"
	// KeyAPICustomEnabled 控制是否启用自定义 api_id / api_hash；
	// 关闭时即使配置了凭据也使用内置 desktop。
	KeyAPICustomEnabled = "api_custom_enabled"
	// KeyDownloadOverwrite 控制下载时是否覆盖同名文件（下载页的「覆盖同名文件」开关）。
	KeyDownloadOverwrite = "download_overwrite"
	// KeyDownloadExpandTextLinks 控制解析时是否展开正文里的文字链接
	// （下载页的「展开正文里的链接」开关）。
	KeyDownloadExpandTextLinks = "download_expand_text_links"
	// KeyDownloadAutoPreview 控制打开下载页时是否自动解析草稿
	// （下载页的「自动解析」行为）。
	KeyDownloadAutoPreview = "download_auto_preview"
	// KeyDownloadComments 控制下载频道帖子时是否同时下载评论区的媒体。
	KeyDownloadComments = "download_comments"
	// KeyDownloadWorkers 是同时下载的文件数（1-8）。
	KeyDownloadWorkers = "download_workers"
	// KeyDownloadThreads 是单个文件的分块下载线程数（1-8）。
	KeyDownloadThreads = "download_threads"
	// KeyWebTheme 是网页主题：light（浅色）/ dark（深色）/ system（跟随系统）。
	KeyWebTheme = "web_theme"
	// KeyWebTitleSpeed 控制是否在浏览器标签标题中显示当前总下载速度。
	KeyWebTitleSpeed = "web_title_speed"

	// DefaultWebTheme 是网页主题未配置时的默认值。
	DefaultWebTheme = "system"
	// DefaultWebTitleSpeed 默认不在标题中显示速度。
	DefaultWebTitleSpeed = "false"

	// MaskedPassword 是配置值里密码被脱敏后的占位符。
	// API 读取代理时返回 ***；写回时若密码仍是它，服务端沿用已保存的密码。
	MaskedPassword = "***"
)

// 配置值类型。
const (
	// TypeString 是普通字符串。
	TypeString = "string"
	// TypeInteger 是十进制整数（存储仍是字符串）。
	TypeInteger = "integer"
	// TypeBoolean 是 true/false（存储仍是字符串）。
	TypeBoolean = "boolean"
)

// Item 描述一个配置项。
type Item struct {
	Key         string
	Type        string
	Default     string
	Description string
	Validate    func(string) error
}

// items 是全部已知配置项，新增配置必须在这里登记。
var items = []Item{
	{
		Key:         KeyHTTPProxy,
		Type:        TypeString,
		Default:     "",
		Description: "出站代理，支持 http/https/socks5/socks5h",
		Validate:    ValidateProxy,
	},
	{
		Key:         KeyHTTPProxyEnabled,
		Type:        TypeBoolean,
		Default:     "true",
		Description: "是否启用 http_proxy；false 时即使配置了代理也直连",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyAPIID,
		Type:        TypeInteger,
		Default:     "",
		Description: "自定义 Telegram api_id（my.telegram.org 申请），与 api_hash 需同时配置；留空使用内置 desktop 凭据",
		Validate:    ValidateAPIID,
	},
	{
		Key:         KeyAPIHash,
		Type:        TypeString,
		Default:     "",
		Description: "自定义 Telegram api_hash，与 api_id 配套；属于敏感信息，请勿泄露",
		Validate:    ValidateAPIHash,
	},
	{
		Key:         KeyAPICustomEnabled,
		Type:        TypeBoolean,
		Default:     "false",
		Description: "是否使用 api_id / api_hash；关闭时即使配置了凭据也使用内置 desktop",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyDownloadOverwrite,
		Type:        TypeBoolean,
		Default:     "false",
		Description: "下载时覆盖同名文件：直接替换目标文件并丢弃已有 .part 从零下载；下载页的「覆盖同名文件」开关即此项",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyDownloadExpandTextLinks,
		Type:        TypeBoolean,
		Default:     "false",
		Description: "解析消息时展开正文里的文字链接（只展开一层）；下载页的「展开正文里的链接」开关即此项",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyDownloadAutoPreview,
		Type:        TypeBoolean,
		Default:     "true",
		Description: "打开下载页且草稿非空时自动解析一次（刷新摘要与封面）；关闭后需手动点「解析预览」",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyDownloadComments,
		Type:        TypeBoolean,
		Default:     "false",
		Description: "通过下载页创建任务时，频道帖子会同时下载评论区里的媒体文件（最多读取前 500 条评论、下载前 100 个媒体；单条媒体下载不会展开评论区）",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyDownloadWorkers,
		Type:        TypeInteger,
		Default:     "1",
		Description: "同时下载的文件数（1-8）；对应启动参数 --max-concurrency，运行中修改对新任务生效",
		Validate:    ValidateDownloadCount,
	},
	{
		Key:         KeyDownloadThreads,
		Type:        TypeInteger,
		Default:     "4",
		Description: "单个文件的分块下载线程数（1-8）；调大可能更容易触发 Telegram 限流",
		Validate:    ValidateDownloadCount,
	},
	{
		Key:         KeyWebTheme,
		Type:        TypeString,
		Default:     DefaultWebTheme,
		Description: "网页主题：light（浅色）/ dark（深色）/ system（跟随系统）",
		Validate:    ValidateWebTheme,
	},
	{
		Key:         KeyWebTitleSpeed,
		Type:        TypeBoolean,
		Default:     DefaultWebTitleSpeed,
		Description: "是否在浏览器标签标题中显示当前总下载速度；无任务时显示「无活动」",
		Validate:    ValidateBool,
	},
	{
		Key:         KeyCurrentAccountID,
		Type:        TypeInteger,
		Default:     "",
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
	HTTPProxy        string
	HTTPProxyEnabled bool
}

// Load 读取全部已知配置，缺失项取 Item.Default。
func Load(db *database.DB) (Settings, error) {
	var settings Settings
	store := NewStore(db)
	value, err := store.String(KeyHTTPProxy)
	if err != nil {
		return settings, err
	}
	enabled, err := store.Bool(KeyHTTPProxyEnabled)
	if err != nil {
		return settings, err
	}
	settings.HTTPProxy = value
	settings.HTTPProxyEnabled = enabled
	return settings, nil
}

// Entry 是一条配置记录。
type Entry struct {
	Key     string
	Type    string
	Default string
	Value   string
	Known   bool // 是否为已知配置项

	Description string // 已知配置项的说明，未知键为空
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

// String 读取字符串配置项；未配置时返回 Item.Default。
func (s *Store) String(key string) (string, error) {
	item, exists := Lookup(key)
	if !exists {
		return "", unknownKeyError(key)
	}
	value, ok, err := s.Get(key)
	if err != nil {
		return "", err
	}
	if !ok {
		return item.Default, nil
	}
	return value, nil
}

// Bool 读取布尔配置项；未配置时返回 Item.Default。
func (s *Store) Bool(key string) (bool, error) {
	item, exists := Lookup(key)
	if !exists {
		return false, unknownKeyError(key)
	}
	if item.Type != TypeBoolean {
		return false, fmt.Errorf("配置项 %s 不是布尔类型", key)
	}

	value, ok, err := s.Get(key)
	if err != nil {
		return false, err
	}
	if !ok {
		value = item.Default
	}
	if value == "" {
		return false, fmt.Errorf("配置项 %s 没有默认值", key)
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("配置项 %s 的值 %q 不是布尔值", key, value)
	}
	return parsed, nil
}

// Int 读取整数配置项；未配置时返回 Item.Default。
func (s *Store) Int(key string) (int, error) {
	item, exists := Lookup(key)
	if !exists {
		return 0, unknownKeyError(key)
	}
	if item.Type != TypeInteger {
		return 0, fmt.Errorf("配置项 %s 不是整数类型", key)
	}

	value, ok, err := s.Get(key)
	if err != nil {
		return 0, err
	}
	if !ok {
		value = item.Default
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("配置项 %s 的值 %q 不是整数", key, value)
	}
	return parsed, nil
}

// Proxy 返回实际生效的代理地址：http_proxy_enabled=false 时返回空串（直连）。
// 两项都未配置时分别取 Item.Default（开关默认启用、地址默认空）。
func (s *Store) Proxy() (string, error) {
	enabled, err := s.Bool(KeyHTTPProxyEnabled)
	if err != nil {
		return "", err
	}
	if !enabled {
		return "", nil
	}

	value, err := s.String(KeyHTTPProxy)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// Set 写入配置项，写入前按配置项规则校验。
func (s *Store) Set(key, value string) error {
	item, exists := Lookup(key)
	if !exists {
		return unknownKeyError(key)
	}
	value = strings.TrimSpace(value)
	if item.Type == TypeBoolean {
		value = strings.ToLower(value)
	}
	if key == KeyWebTheme {
		// 主题值统一存小写，避免 light / Light 在数据库里出现两种写法。
		value = strings.ToLower(value)
	}
	if item.Validate != nil {
		if err := item.Validate(value); err != nil {
			return err
		}
	}
	return s.db.SetSettingTyped(key, item.Type, value)
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
		item, known := Lookup(row.Key)
		valueType := row.Type
		defaultValue := ""
		if known {
			// 已知键以代码里的定义为准，避免历史数据没有类型或类型写错。
			valueType = item.Type
			defaultValue = item.Default
		}
		if valueType == "" {
			valueType = TypeString
		}
		entry := Entry{
			Key:     row.Key,
			Type:    valueType,
			Default: defaultValue,
			Value:   strings.TrimSpace(row.Value),
			Known:   known,
		}
		if known {
			entry.Description = item.Description
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// SyncTypes 把已登记配置项的类型写回数据库。
//
// 旧版本没有 settings.type 列；AutoMigrate 会补上默认值 string，
// 启动时调用本函数把 current_account_id 等已知键修正为正确类型。
func SyncTypes(db *database.DB) error {
	for _, item := range items {
		valueType := item.Type
		if valueType == "" {
			valueType = TypeString
		}
		if err := db.Model(&database.Setting{}).
			Where("key = ? AND (type IS NULL OR type <> ?)", item.Key, valueType).
			UpdateColumn("type", valueType).Error; err != nil {
			return fmt.Errorf("同步配置项 %s 的类型: %w", item.Key, err)
		}
	}
	return nil
}

// SeedDefaults 在 settings 表完全为空时写入带非空默认值的配置项。
//
// 空默认值（http_proxy、current_account_id）表示「未配置」，不落库；
// 目前会写入 api_custom_enabled=false 与 http_proxy_enabled=true。
// 已有任意配置时不做任何事，避免把用户主动清除的配置重新写回来。
func SeedDefaults(db *database.DB) error {
	entries, err := db.ListSettings()
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	if len(entries) > 0 {
		return nil
	}

	store := NewStore(db)
	for _, item := range items {
		if item.Default == "" {
			continue
		}
		if err := store.Set(item.Key, item.Default); err != nil {
			return fmt.Errorf("写入默认配置 %s: %w", item.Key, err)
		}
	}
	return nil
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

// ValidateBool 校验布尔配置项：接受 true / false（大小写不敏感）。
func ValidateBool(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "false":
		return nil
	}
	return fmt.Errorf("只接受 true 或 false，当前为 %q", raw)
}

// ValidateWebTheme 校验网页主题：light / dark / system（大小写不敏感）。
func ValidateWebTheme(raw string) error {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "light", "dark", "system":
		return nil
	}
	return fmt.Errorf("只接受 light、dark 或 system，当前为 %q", raw)
}

// ValidateDownloadCount 校验下载并发数（并行文件数/线程数）：1-8 的整数。
func ValidateDownloadCount(raw string) error {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 || value > 8 {
		return fmt.Errorf("必须是 1-8 的整数，当前为 %q", raw)
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

// ValidateAPIID 校验 api_id：必须是正整数，且放得进 int32。
func ValidateAPIID(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("api_id 不能为空；如需清除请使用 config unset api_id")
	}
	id, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || id <= 0 {
		return fmt.Errorf("api_id 必须是正整数，当前为 %q", raw)
	}
	return nil
}

// ValidateAPIHash 校验 api_hash：32 位十六进制字符串。
func ValidateAPIHash(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("api_hash 不能为空；如需清除请使用 config unset api_hash")
	}
	if len(raw) != 32 {
		return fmt.Errorf("api_hash 应为 32 位十六进制字符串（当前长度 %d）", len(raw))
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return errors.New("api_hash 只能包含 0-9 与 a-f")
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
		u.User = url.UserPassword(u.User.Username(), MaskedPassword)
	}
	masked := u.String()
	// net/url 会把 userinfo 里的 * 转义成全大写的 %2A，展示与前端回填都
	// 更希望看到字面量 ***，这里还原（仅影响展示字符串）。
	masked = strings.ReplaceAll(masked, "%2A%2A%2A", MaskedPassword)
	return masked
}

// MergeMaskedProxy 处理「读取时脱敏、写回时保留密码」的编辑场景。
//
// API 读取 http_proxy 时密码是 ***；用户只改了主机/端口并把 *** 原样提交回来时，
// 用 current 中的真实密码替换，避免把 *** 写成新密码。
// incoming 没有密码或密码不是 *** 时原样返回。
func MergeMaskedProxy(current, incoming string) (string, error) {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return "", errors.New("代理地址不能为空")
	}

	next, err := url.Parse(incoming)
	if err != nil {
		return "", fmt.Errorf("代理地址 %q 不是合法 URL: %w", incoming, err)
	}
	if next.User == nil {
		return incoming, nil
	}
	password, hasPassword := next.User.Password()
	if !hasPassword || password != MaskedPassword {
		return incoming, nil
	}

	if strings.TrimSpace(current) == "" {
		return "", errors.New("代理密码已脱敏，请重新输入完整代理地址")
	}
	prev, err := url.Parse(current)
	if err != nil || prev.User == nil {
		return "", errors.New("代理密码已脱敏，请重新输入完整代理地址")
	}
	prevPassword, ok := prev.User.Password()
	if !ok || prevPassword == "" {
		return "", errors.New("代理密码已脱敏，请重新输入完整代理地址")
	}

	next.User = url.UserPassword(next.User.Username(), prevPassword)
	return next.String(), nil
}
