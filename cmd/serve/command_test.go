package serve

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/server"
)

func TestListenErrorTranslation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "Windows 端口占用",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: errors.New("bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.")},
			want: "端口已被占用",
		},
		{
			name: "Unix 端口占用",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: errors.New("bind: address already in use")},
			want: "端口已被占用",
		},
		{
			name: "Windows 保留端口",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: errors.New("bind: An attempt was made to access a socket in a way forbidden by its access permissions.")},
			want: "系统保留",
		},
		{
			name: "其他错误原样返回",
			err:  &net.OpError{Op: "listen", Net: "tcp", Err: errors.New("network is unreachable")},
			want: "监听",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenError("127.0.0.1:22429", tt.err)
			if !strings.Contains(got.Error(), tt.want) {
				t.Errorf("错误 = %q，期望包含 %q", got, tt.want)
			}
		})
	}
}

func TestServeUntilSignalShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("侦听测试端口: %v", err)
	}
	srv := server.New(server.Config{Addr: ln.Addr().String()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- serveUntilSignal(ctx, srv, ln, ln.Addr().String()) }()

	// 等健康检查通过，确保 Serve 真的跑起来了再取消。
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务未就绪: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("优雅关闭返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("优雅关闭超时")
	}
}
