package mtproto

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gotd/td/telegram/dcs"
	"golang.org/x/net/proxy"
)

// newProxyDialer 根据代理 URL 构造拨号函数；空字符串表示直连。
//
// 支持 socks5 / socks5h（远端 DNS）与 http / https（CONNECT 隧道）。
func newProxyDialer(raw string) (dcs.DialFunc, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析代理地址 %q: %w", raw, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("代理地址 %q 缺少主机与端口", raw)
	}

	var auth *proxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: password}
	}

	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS5 拨号器: %w", err)
		}
		if cd, ok := d.(proxy.ContextDialer); ok {
			return func(ctx context.Context, network, addr string) (net.Conn, error) {
				return cd.DialContext(ctx, network, addr)
			}, nil
		}
		return func(_ context.Context, network, addr string) (net.Conn, error) {
			return d.Dial(network, addr)
		}, nil

	case "http", "https":
		return newHTTPConnectDialer(u, auth), nil

	default:
		return nil, fmt.Errorf("不支持的代理协议 %q（仅支持 socks5/socks5h/http/https）", u.Scheme)
	}
}

// newHTTPConnectDialer 通过 HTTP CONNECT 建立隧道；https 表示先与代理做 TLS 握手。
func newHTTPConnectDialer(u *url.URL, auth *proxy.Auth) dcs.DialFunc {
	useTLS := strings.EqualFold(u.Scheme, "https")
	base := &net.Dialer{Timeout: 30 * time.Second}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := base.DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return nil, fmt.Errorf("连接代理 %s: %w", u.Host, err)
		}

		if useTLS {
			host := u.Hostname()
			tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("与代理 %s 的 TLS 握手失败: %w", u.Host, err)
			}
			conn = tlsConn
		}

		req := &http.Request{
			Method: http.MethodConnect,
			URL:    &url.URL{Opaque: addr},
			Host:   addr,
			Header: make(http.Header),
		}
		req.Header.Set("Proxy-Connection", "Keep-Alive")
		if auth != nil {
			cred := base64.StdEncoding.EncodeToString([]byte(auth.User + ":" + auth.Password))
			req.Header.Set("Proxy-Authorization", "Basic "+cred)
		}

		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		if err := req.Write(conn); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("写入 CONNECT 请求: %w", err)
		}

		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("读取代理响应: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("代理 CONNECT 失败: %s", resp.Status)
		}
		_ = conn.SetDeadline(time.Time{})

		if br.Buffered() > 0 {
			return &bufferedConn{Conn: conn, r: br}, nil
		}
		return conn, nil
	}
}

// bufferedConn 保留 bufio 中已预读的数据，避免隧道里的首包丢失。
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// dcsPlain 把拨号器包装成 DC 解析器，交给 telegram.Options.Resolver。
func dcsPlain(dial dcs.DialFunc) dcs.Resolver {
	return dcs.Plain(dcs.PlainOptions{Dial: dial})
}

// maskProxy 隐藏代理 URL 中的账号密码，用于日志与终端提示。
func maskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User("***")
	return u.String()
}
