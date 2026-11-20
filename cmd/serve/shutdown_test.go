package serve

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/server"
)

// TestServeUntilSignalClosesSSE 模拟 Ctrl+C（取消父 context）：
// 有 SSE 长连接时也要立即退出并返回 nil，而不是等满 10s 报「优雅关闭超时」。
func TestServeUntilSignalClosesSSE(t *testing.T) {
	hub := server.NewEventHub()
	srv := server.New(server.Config{EventHub: hub})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- serveUntilSignal(ctx, srv, ln, ln.Addr().String()) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1/events")
	if err != nil {
		t.Fatalf("连接 SSE 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("读取 SSE 帧失败: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("关闭应成功，实际报错: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("3s 内没有退出，优雅关闭被 SSE 卡住")
	}
}

// TestSignalName 确认信号能打印成可读类型。
func TestSignalName(t *testing.T) {
	if got := signalName(os.Interrupt); !strings.Contains(got, "Ctrl+C") {
		t.Errorf("Interrupt = %q，期望包含 Ctrl+C", got)
	}
	if got := signalName(syscall.SIGTERM); !strings.Contains(got, "SIGTERM") {
		t.Errorf("SIGTERM = %q", got)
	}
	if got := signalName(syscall.SIGHUP); got != syscall.SIGHUP.String() {
		t.Errorf("未知信号 = %q，期望 %q", got, syscall.SIGHUP.String())
	}
}
