package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParseFormat 覆盖格式解析与别名。
func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"json":    FormatJSON,
		"JSON":    FormatJSON,
		"yaml":    FormatYAML,
		"yml":     FormatYAML,
		"toml":    FormatTOML,
		"  TOML ": FormatTOML,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil {
			t.Fatalf("ParseFormat(%q) 报错: %v", in, err)
		}
		if got != want {
			t.Errorf("ParseFormat(%q) = %q，期望 %q", in, got, want)
		}
	}
	if _, err := ParseFormat("ini"); err == nil {
		t.Error("不支持的格式应报错")
	}
}

// prepareStore 写入两条配置供导出测试使用。
func prepareStore(t *testing.T) *Store {
	t.Helper()
	store := openStore(t)
	if err := store.Set(KeyHTTPProxy, "http://user:pass@127.0.0.1:7897"); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	return store
}

// TestExportJSON 确认 JSON 导出可被正确解析。
func TestExportJSON(t *testing.T) {
	store := prepareStore(t)

	data, err := store.ExportJSON()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("JSON 应以换行结尾")
	}

	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("解析导出内容失败: %v", err)
	}
	if decoded[KeyHTTPProxy] != "http://user:pass@127.0.0.1:7897" {
		t.Errorf("http_proxy = %q", decoded[KeyHTTPProxy])
	}
}

// TestExportYAML 确认 YAML 导出可被正确解析。
func TestExportYAML(t *testing.T) {
	store := prepareStore(t)

	data, err := store.ExportYAML()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}

	var decoded map[string]string
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("解析导出内容失败: %v", err)
	}
	if decoded[KeyHTTPProxy] != "http://user:pass@127.0.0.1:7897" {
		t.Errorf("http_proxy = %q", decoded[KeyHTTPProxy])
	}
}

// TestExportTOML 确认 TOML 输出的基本形态与转义。
func TestExportTOML(t *testing.T) {
	store := openStore(t)
	if err := store.Set(KeyHTTPProxy, `http://127.0.0.1:7897?a="b"\c`); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	data, err := store.ExportTOML()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	out := string(data)

	if !strings.Contains(out, `http_proxy = "`) {
		t.Errorf("缺少 http_proxy 行: %s", out)
	}
	if !strings.Contains(out, `\"`) {
		t.Errorf("双引号未转义: %s", out)
	}
	if !strings.Contains(out, `\\`) {
		t.Errorf("反斜杠未转义: %s", out)
	}
}

// TestExportUnsupported 确认未知格式报错。
func TestExportUnsupported(t *testing.T) {
	store := openStore(t)
	if _, err := store.Export("ini"); err == nil {
		t.Error("未知格式应报错")
	}
}

// TestTomlEncoding 覆盖键与值的转义边界。
func TestTomlEncoding(t *testing.T) {
	if got := tomlKey("http_proxy"); got != "http_proxy" {
		t.Errorf("裸键 = %q", got)
	}
	if got := tomlKey("a.b"); got != `"a.b"` {
		t.Errorf("含点键 = %q", got)
	}
	if got := tomlString("line1\nline2\t\"q\""); got != `"line1\nline2\t\"q\""` {
		t.Errorf("转义结果 = %q", got)
	}
	if got := tomlString("\x01"); got != `"\u0001"` {
		t.Errorf("控制字符 = %q", got)
	}
}
