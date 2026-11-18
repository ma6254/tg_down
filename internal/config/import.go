package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// FormatFromPath 根据文件扩展名推断格式。
func FormatFromPath(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return FormatJSON, nil
	case ".yaml", ".yml":
		return FormatYAML, nil
	case ".toml":
		return FormatTOML, nil
	default:
		return "", fmt.Errorf("无法从扩展名推断 %q 的格式，请用 --format 指定", path)
	}
}

// ImportResult 描述一次导入的结果。
type ImportResult struct {
	Updated int      // 写入或更新的配置项数量
	Removed int      // replace 模式下被删除的已知配置项数量
	Skipped []string // 文件里存在、但未登记的键
}

// Import 把配置合并进数据库：只覆盖文件中出现的已知配置项。
func (s *Store) Import(format Format, data []byte) (ImportResult, error) {
	return s.importData(format, data, false)
}

// Replace 用文件内容重置已知配置项：文件里没有的已知项会被删除。
func (s *Store) Replace(format Format, data []byte) (ImportResult, error) {
	return s.importData(format, data, true)
}

// importData 是 Import / Replace 的共同实现。
func (s *Store) importData(format Format, data []byte, replace bool) (ImportResult, error) {
	values, err := decodeSettings(format, data)
	if err != nil {
		return ImportResult{}, err
	}
	if err := validateImport(values); err != nil {
		return ImportResult{}, err
	}

	var result ImportResult
	known := make(map[string]string, len(values))
	for key, value := range values {
		if _, ok := Lookup(key); !ok {
			result.Skipped = append(result.Skipped, key)
			continue
		}
		known[key] = value
	}

	if replace {
		existing, err := s.List()
		if err != nil {
			return result, err
		}
		for _, entry := range existing {
			if _, ok := Lookup(entry.Key); !ok {
				continue
			}
			if _, present := known[entry.Key]; present {
				continue
			}
			if err := s.Unset(entry.Key); err != nil {
				return result, err
			}
			result.Removed++
		}
	}

	for key, value := range known {
		if err := s.Set(key, value); err != nil {
			return result, err
		}
		result.Updated++
	}

	return result, nil
}

// decodeSettings 把各格式的配置内容解析成键值对。
func decodeSettings(format Format, data []byte) (map[string]string, error) {
	switch format {
	case FormatJSON:
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("解析 JSON: %w", err)
		}
		return toStringMap(raw)

	case FormatYAML:
		var raw map[string]any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("解析 YAML: %w", err)
		}
		return toStringMap(raw)

	case FormatTOML:
		var raw map[string]any
		if err := toml.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("解析 TOML: %w", err)
		}
		return toStringMap(raw)

	default:
		return nil, fmt.Errorf("不支持的导出格式 %q，可用：json、yaml、toml", format)
	}
}

// toStringMap 把解析结果收敛成字符串键值对，拒绝嵌套结构与非字符串值。
func toStringMap(raw map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		switch v := value.(type) {
		case string:
			out[key] = v
		case nil:
			out[key] = ""
		default:
			return nil, fmt.Errorf("配置项 %q 的值必须是字符串，实际为 %T", key, value)
		}
	}
	return out, nil
}

// errEmptyConfig 表示导入内容为空。
var errEmptyConfig = errors.New("配置文件里没有任何配置项")

// validateImport 在导入前做一次空内容检查。
func validateImport(values map[string]string) error {
	if len(values) == 0 {
		return errEmptyConfig
	}
	return nil
}
