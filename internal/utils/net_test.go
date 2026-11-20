package utils

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

// TestIsAddrInUse 覆盖 Unix errno、Windows 错误码与纯文案三种来源。
func TestIsAddrInUse(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			name: "Unix EADDRINUSE",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE},
			want: true,
		},
		{
			name: "Windows WSAEADDRINUSE",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: syscall.Errno(10048)},
			want: true,
		},
		{
			name: "Windows 错误文案",
			err:  errors.New("bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."),
			want: true,
		},
		{
			name: "Unix 错误文案",
			err:  errors.New("bind: address already in use"),
			want: true,
		},
		{
			name: "其他网络错误",
			err:  errors.New("network is unreachable"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAddrInUse(tt.err); got != tt.want {
				t.Errorf("IsAddrInUse(%v) = %v，期望 %v", tt.err, got, tt.want)
			}
		})
	}
}
