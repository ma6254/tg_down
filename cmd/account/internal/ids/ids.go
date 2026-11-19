// Package ids 解析账号子命令里的账号 ID 参数。
package ids

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse 解析并校验账号 ID；只接受纯数字，避免手机号被当成 ID。
func Parse(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("账号 ID 不能为空（可用 account list 查看）")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("账号 ID 必须是纯数字，当前为 %q（可用 account list 查看）", raw)
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("账号 ID 必须是正整数，当前为 %q（可用 account list 查看）", raw)
	}
	return id, nil
}
