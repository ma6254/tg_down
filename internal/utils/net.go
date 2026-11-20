package utils

import (
	"errors"
	"strings"
	"syscall"
)

// wsaEADDRINUSE 是 Windows 的“端口已占用”错误码（10048）。
// Go 只把 syscall.EADDRINUSE 映射为应用错误，不覆盖 WSA 错误码，
// 所以这里单独判断，兼顾 Unix 与 Windows。
const wsaEADDRINUSE = 10048

// IsAddrInUse 判断错误是否为“地址已被占用”。
//
// 先按 errno 判断（覆盖 Unix 的 EADDRINUSE 与 Windows 的 WSAEADDRINUSE），
// 再退回错误文案匹配：部分平台/包装层只保留文字，丢失了 errno。
func IsAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && (int(errno) == wsaEADDRINUSE || errors.Is(errno, syscall.EADDRINUSE)) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "in use") || strings.Contains(msg, "only one usage")
}
