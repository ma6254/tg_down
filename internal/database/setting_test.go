package database

import (
	"errors"
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
