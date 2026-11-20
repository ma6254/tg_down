package utils

import "fmt"

// FormatBytes 把字节数格式化为便于阅读的单位（1024 进制）。
func FormatBytes(n int64) string {
	if n < 0 {
		return "-" + FormatBytes(-n)
	}

	const unit = int64(1024)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := unit, 0
	for n >= div*unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
