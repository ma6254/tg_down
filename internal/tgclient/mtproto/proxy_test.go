package mtproto

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// startFakeProxy 启动一个最小 HTTP CONNECT 代理：接受 CONNECT 后回 200，
// 然后把后续字节回显给客户端。
func startFakeProxy(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()

				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method != http.MethodConnect {
					_, _ = io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")

				// 回显，供测试验证隧道可用。
				_, _ = io.Copy(c, br)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() { _ = ln.Close() }
}

// TestHTTPConnectDialer 验证 http 代理隧道能收发数据。
func TestHTTPConnectDialer(t *testing.T) {
	addr, closeFn := startFakeProxy(t)
	defer closeFn()

	dial, err := newProxyDialer("http://" + addr)
	if err != nil {
		t.Fatalf("构造拨号器失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", "149.154.167.51:443")
	if err != nil {
		t.Fatalf("通过代理建连失败: %v", err)
	}
	defer conn.Close()

	const payload = "ping-through-proxy"
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	buf := make([]byte, len(payload))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取回显失败: %v", err)
	}
	if string(buf) != payload {
		t.Fatalf("回显内容 = %q，期望 %q", buf, payload)
	}
}

// TestNewProxyDialer 覆盖代理 URL 的解析与错误分支。
func TestNewProxyDialer(t *testing.T) {
	if d, err := newProxyDialer(""); d != nil || err != nil {
		t.Fatalf("空代理应返回 nil,nil，实际 %v,%v", d, err)
	}
	if _, err := newProxyDialer("socks5://127.0.0.1:1080"); err != nil {
		t.Fatalf("socks5 代理解析失败: %v", err)
	}
	if _, err := newProxyDialer("http://user:pass@127.0.0.1:7897"); err != nil {
		t.Fatalf("带认证的 http 代理解析失败: %v", err)
	}
	if _, err := newProxyDialer("ftp://127.0.0.1:21"); err == nil {
		t.Fatal("不支持的协议应报错")
	}
	if _, err := newProxyDialer("http://"); err == nil {
		t.Fatal("缺少主机应报错")
	}
}

// TestMaskProxy 确认代理凭据不会出现在提示里。
func TestMaskProxy(t *testing.T) {
	got := maskProxy("http://user:pass@127.0.0.1:7897")
	if got == "http://user:pass@127.0.0.1:7897" {
		t.Fatalf("代理凭据未被脱敏: %s", got)
	}
	if got != "http://%2A%2A%2A@127.0.0.1:7897" && got != "http://***@127.0.0.1:7897" {
		t.Logf("脱敏结果: %s", got)
	}
}
