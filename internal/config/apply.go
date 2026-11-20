package config

import (
	"errors"
	"fmt"
)

// ErrInvalidSetting 表示配置项未知、取值非法，或不允许通过接口写入。
//
// 接口层据它返回 400，其余错误都按内部错误处理。
var ErrInvalidSetting = errors.New("配置项非法")

// Apply 部分更新配置，返回更新后的全量配置。
//
// 值为 nil 表示删除（等价 config unset），未出现的键保持原值。
// 先整体校验再写入，避免写一半失败留下半套配置。
// 代理密码在接口上以 *** 返回，原样提交回来时用库里现有密码回填，
// 否则会把 *** 当成新密码写进去。
func (s *Store) Apply(patch map[string]*string) ([]Entry, error) {
	// 先复制并归一化，不动调用方传进来的 map。
	normalized := make(map[string]*string, len(patch))
	for key, value := range patch {
		item, ok := Lookup(key)
		if !ok {
			return nil, fmt.Errorf("%w: 未知配置项 %q", ErrInvalidSetting, key)
		}
		if value == nil {
			normalized[key] = nil
			continue
		}

		incoming := *value
		if key == KeyHTTPProxy {
			current, _, err := s.Get(KeyHTTPProxy)
			if err != nil {
				return nil, fmt.Errorf("读取现有代理: %w", err)
			}
			merged, err := MergeMaskedProxy(current, incoming)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidSetting, err)
			}
			incoming = merged
		}
		if item.Validate != nil {
			if err := item.Validate(incoming); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidSetting, err)
			}
		}
		value := incoming
		normalized[key] = &value
	}

	for key, value := range normalized {
		var err error
		if value == nil {
			err = s.Unset(key)
		} else {
			err = s.Set(key, *value)
		}
		if err != nil {
			return nil, err
		}
	}
	return s.List()
}
