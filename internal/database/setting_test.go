package database

import (
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

// TestSettingCRUD 覆盖配置项的读写与删除。
func TestSettingCRUD(t *testing.T) {
	db := openTestDB(t)

	if err := db.SetSetting("http_proxy", "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	got, err := db.GetSetting("http_proxy")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got != "http://127.0.0.1:7897" {
		t.Errorf("value = %q", got)
	}

	// 更新同一键。
	if err := db.SetSetting("http_proxy", "socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	got, err = db.GetSetting("http_proxy")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got != "socks5://127.0.0.1:1080" {
		t.Errorf("更新后 value = %q", got)
	}

	items, err := db.ListSettings()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("应有 1 条配置，实际 %d", len(items))
	}

	if err := db.DeleteSetting("http_proxy"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := db.GetSetting("http_proxy"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("删除后应返回 ErrRecordNotFound，实际 %v", err)
	}

	// 删除不存在的键不应报错。
	if err := db.DeleteSetting("nope"); err != nil {
		t.Errorf("删除不存在的键应无错: %v", err)
	}
}

// TestSettingType 覆盖带类型写入与读取。
func TestSettingType(t *testing.T) {
	db := openTestDB(t)

	if err := db.SetSettingTyped("current_account_id", "integer", "7"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := db.SetSetting("http_proxy", "http://127.0.0.1:7897"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	items, err := db.ListSettings()
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	types := make(map[string]string, len(items))
	for _, item := range items {
		types[item.Key] = item.Type
	}
	if types["current_account_id"] != "integer" {
		t.Errorf("current_account_id type = %q", types["current_account_id"])
	}
	if types["http_proxy"] != "string" {
		t.Errorf("http_proxy type = %q", types["http_proxy"])
	}
}

// TestInstallAddsSettingType 确认旧库（没有 type 列）能被 AutoMigrate 补列并给默认值。
func TestInstallAddsSettingType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	// 模拟旧版本只有 key/value/updated_at 的 settings 表。
	if err := db.Exec("CREATE TABLE settings (key text PRIMARY KEY, value text, updated_at datetime)").Error; err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if err := db.Exec("INSERT INTO settings (key, value) VALUES ('http_proxy', 'http://127.0.0.1:7897')").Error; err != nil {
		t.Fatalf("写入旧数据失败: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var item Setting
	if err := db.First(&item, "key = ?", "http_proxy").Error; err != nil {
		t.Fatalf("读取旧数据失败: %v", err)
	}
	if item.Type != DefaultSettingType {
		t.Errorf("旧数据 type = %q，期望默认 %q", item.Type, DefaultSettingType)
	}
}
