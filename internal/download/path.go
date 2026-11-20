// Package download 负责下载文件的落盘规划：目录、文件名清洗、断点续传位置。
//
// 这里只做纯本地计算，不访问网络；实际传输在 internal/tgclient/mtproto。
package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PartSize 是断点续传的对齐粒度，与 Telegram 单次 upload.getFile 上限一致（1 MiB）。
const PartSize = 1024 * 1024

// Plan 描述一个文件最终写到哪里、从哪里续传。
type Plan struct {
	FinalPath string // 下载完成后的路径
	PartPath  string // 下载中的 .part 路径
	Offset    int64  // 已下载字节数，按 PartSize 对齐
	Skip      bool   // 目标文件已存在且大小一致
}

// NewPlan 计算落盘位置。
//
// 规则：
//   - 目录为 <root>/<会话标题>（flat 时直接用 <root>）；
//   - 文件名清洗非法字符并限制长度；
//   - 已存在同名且大小一致的文件视为完成（force 时忽略）；
//   - 同名但大小不同时追加 " (1)" 等后缀，避免覆盖；
//   - 已有 .part 时按对齐后的体积续传。
func NewPlan(root, chatTitle, fileName string, size int64, flat, force bool) (Plan, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "downloads"
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Plan{}, fmt.Errorf("创建下载目录 %s: %w", root, err)
	}

	name := SafeName(fileName, 180)
	if name == "" {
		name = "file"
	}

	dir := root
	if !flat {
		dir = filepath.Join(root, SafeName(chatTitle, 80))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Plan{}, fmt.Errorf("创建下载目录 %s: %w", dir, err)
	}

	final := filepath.Join(dir, name)
	if !force {
		if info, err := os.Stat(final); err == nil && !info.IsDir() && size > 0 && info.Size() == size {
			return Plan{FinalPath: final, Skip: true}, nil
		}
	}
	if _, err := os.Stat(final); err == nil {
		final = availablePath(final)
	}

	plan := Plan{FinalPath: final, PartPath: final + ".part"}
	if force {
		return plan, nil
	}

	if info, err := os.Stat(plan.PartPath); err == nil && !info.IsDir() {
		offset := info.Size()
		if size > 0 && offset > size {
			offset = size
		}
		offset -= offset % PartSize
		if offset < 0 {
			offset = 0
		}
		plan.Offset = offset
	}
	return plan, nil
}

// SafeName 清洗文件名：替换 Windows 非法字符与控制字符、去掉首尾空格和点，
// 处理保留设备名，并按字符（而非字节）截断到 max 个字符。
func SafeName(raw string, max int) string {
	const invalid = `<>:"/\|?*`

	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		if r < 32 || strings.ContainsRune(invalid, r) {
			b.WriteRune('_')
			continue
		}
		b.WriteRune(r)
	}

	name := strings.Trim(b.String(), " .")
	if name == "" {
		return ""
	}

	base := strings.TrimSuffix(name, filepath.Ext(name))
	if isReservedName(base) {
		name = "_" + name
	}

	if max > 0 {
		runes := []rune(name)
		if len(runes) > max {
			name = string(runes[:max])
			name = strings.TrimRight(name, " .")
		}
	}
	return name
}

// isReservedName 判断 Windows 保留设备名（CON、NUL、COM1 等）。
func isReservedName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(upper) == 4 {
		prefix := upper[:3]
		digit := upper[3]
		if (prefix == "COM" || prefix == "LPT") && digit >= '1' && digit <= '9' {
			return true
		}
	}
	return false
}

// availablePath 在同目录下找一个不存在的名字：`name (1).ext`、`name (2).ext`…
func availablePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}

	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}
