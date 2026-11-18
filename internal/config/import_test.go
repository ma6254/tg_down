package config

import (
	"strings"
	"testing"
)

// TestFormatFromPath 覆盖扩展名推断。
func TestFormatFromPath(t *testing.T) {
	cases := map[string]Format{
		"config.json": FormatJSON,
		"config.yaml": FormatYAML,
		"config.yml":  FormatYAML,
		"config.toml": FormatTOML,
		"CONFIG.JSON": FormatJSON,
		"a/b/c.toml":  FormatTOML,
	}
	for path, want := range cases {
		got, err := FormatFromPath(path)
		if err != nil {
			t.Fatalf("FormatFromPath(%q) 报错: %v", path, err)
		}
		if got != want {
			t.Errorf("FormatFromPath(%q) = %q，期望 %q", path, got, want)
		}
	}

	if _, err := FormatFromPath("config.ini"); err == nil {
		t.Error("未知扩展名应报错")
	}
}

// TestImportMergeJSON 确认合并导入只覆盖已有项，未登记键被跳过。
func TestImportMergeJSON(t *testing.T) {
	store := openStore(t)
	if err := store.Set(KeyHTTPProxy, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}

	data := []byte(`{"http_proxy":"http://127.0.0.1:7897","unknown_key":"x"}`)
	result, err := store.Import(FormatJSON, data)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if result.Updated != 1 {
		t.Errorf("Updated = %d", result.Updated)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "unknown_key" {
		t.Errorf("Skipped = %v", result.Skipped)
	}

	value, ok, err := store.Get(KeyHTTPProxy)
	if err != nil || !ok {
		t.Fatalf("读取失败: ok=%v err=%v", ok, err)
	}
	if value != "http://127.0.0.1:7897" {
		t.Errorf("value = %q", value)
	}
}

// TestImportReplace 确认 replace 模式会删除文件中缺失的已知项。
func TestImportReplace(t *testing.T) {
	store := openStore(t)
	if err := store.Set(KeyHTTPProxy, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("准备配置失败: %v", err)
	}

	// 文件只含未登记键：已知项应被清空。
	data := []byte("unknown_key: x\n")
	result, err := store.Replace(FormatYAML, data)
	if err != nil {
		t.Fatalf("导入失败: %v", err)
	}
	if result.Removed != 1 {
		t.Errorf("Removed = %d", result.Removed)
	}
	if _, ok, err := store.Get(KeyHTTPProxy); err != nil || ok {
		t.Errorf("http_proxy 应被删除: ok=%v err=%v", ok, err)
	}
}

// TestImportRoundTrip 确认导出后能原样导入。
func TestImportRoundTrip(t *testing.T) {
	formats := []Format{FormatJSON, FormatYAML, FormatTOML}
	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			source := openStore(t)
			if err := source.Set(KeyHTTPProxy, "socks5h://user:pass@127.0.0.1:1080"); err != nil {
				t.Fatalf("准备配置失败: %v", err)
			}
			data, err := source.Export(format)
			if err != nil {
				t.Fatalf("导出失败: %v", err)
			}

			target := openStore(t)
			result, err := target.Import(format, data)
			if err != nil {
				t.Fatalf("导入失败: %v", err)
			}
			if result.Updated != 1 {
				t.Errorf("Updated = %d", result.Updated)
			}

			value, ok, err := target.Get(KeyHTTPProxy)
			if err != nil || !ok {
				t.Fatalf("读取失败: ok=%v err=%v", ok, err)
			}
			if value != "socks5h://user:pass@127.0.0.1:1080" {
				t.Errorf("value = %q", value)
			}
		})
	}
}

// TestImportInvalidValue 确认非字符串值被拒绝。
func TestImportInvalidValue(t *testing.T) {
	store := openStore(t)
	_, err := store.Import(FormatJSON, []byte(`{"http_proxy":123}`))
	if err == nil {
		t.Fatal("非字符串值应报错")
	}
	if !strings.Contains(err.Error(), "必须是字符串") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}

// TestImportEmpty 确认空配置被拒绝。
func TestImportEmpty(t *testing.T) {
	store := openStore(t)
	if _, err := store.Import(FormatJSON, []byte(`{}`)); err == nil {
		t.Fatal("空配置应报错")
	}
}

// TestImportInvalidProxy 确认导入时仍走配置项校验。
func TestImportInvalidProxy(t *testing.T) {
	store := openStore(t)
	if _, err := store.Import(FormatJSON, []byte(`{"http_proxy":"ftp://x"}`)); err == nil {
		t.Fatal("非法代理应报错")
	}
}

// TestImportUnknownFormat 确认未知格式报错。
func TestImportUnknownFormat(t *testing.T) {
	store := openStore(t)
	if _, err := store.Import("ini", []byte("x")); err == nil {
		t.Fatal("未知格式应报错")
	}
}
