package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format 是配置导出格式。
type Format string

// 支持的导出格式。
const (
	FormatJSON Format = "json"
	FormatYAML Format = "yaml"
	FormatTOML Format = "toml"
)

// ParseFormat 解析格式名，兼容 yml 简写。
func ParseFormat(raw string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "json":
		return FormatJSON, nil
	case "yaml", "yml":
		return FormatYAML, nil
	case "toml":
		return FormatTOML, nil
	default:
		return "", fmt.Errorf("不支持的导出格式 %q，可用：json、yaml、toml", raw)
	}
}

// Snapshot 返回配置的键值快照（包含未登记的键）。
func (s *Store) Snapshot() (map[string]string, error) {
	entries, err := s.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		out[entry.Key] = entry.Value
	}
	return out, nil
}

// Export 按指定格式导出配置。
func (s *Store) Export(format Format) ([]byte, error) {
	switch format {
	case FormatJSON:
		return s.ExportJSON()
	case FormatYAML:
		return s.ExportYAML()
	case FormatTOML:
		return s.ExportTOML()
	default:
		return nil, fmt.Errorf("不支持的导出格式 %q", format)
	}
}

// ExportJSON 导出为缩进 JSON。
func (s *Store) ExportJSON() ([]byte, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ExportYAML 导出为 YAML。
func (s *Store) ExportYAML() ([]byte, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ExportTOML 导出为 TOML。
//
// 配置是扁平的字符串键值对，这里手写编码以保证输出顺序稳定且转义符合 TOML 规范。
func (s *Store) ExportTOML() ([]byte, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&builder, "%s = %s\n", tomlKey(key), tomlString(snapshot[key]))
	}
	return []byte(builder.String()), nil
}

// tomlKey 输出合法的 TOML 键：能裸写就裸写，否则加引号。
func tomlKey(key string) string {
	if key == "" {
		return `""`
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return tomlString(key)
		}
	}
	return key
}

// tomlString 输出 TOML 基本字符串，处理引号、反斜杠与控制字符。
func tomlString(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&builder, `\u%04X`, r)
				continue
			}
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}
