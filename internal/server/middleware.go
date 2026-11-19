package server

import (
	"bufio"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// statusRecorder 包装 ResponseWriter 以记录状态码。
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// Flush 转发到下层 writer，保证 SSE 等流式响应不被中间件破坏。
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 支持 http.ResponseController 访问底层 writer。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack 转发 WebSocket 升级所需的连接接管。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer 不支持 Hijack")
	}
	return hijacker.Hijack()
}

// loggingMiddleware 记录每个请求的方法、路径、状态码与耗时。
// 不记录 header 与请求体，避免把 token 打进日志。
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("http %s %s %d %.1fms", r.Method, r.URL.Path, rec.status,
			float64(time.Since(start).Microseconds())/1000)
	})
}

// recoverMiddleware 捕获 handler panic，避免整个进程退出。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("http panic: %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				writeError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeaders 追加基础安全响应头。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// bearerAuth 对 /api/* 校验 Bearer token；静态资源不需要鉴权。
// token 为空表示只监听回环地址，直接放行。
func bearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 静态资源与接口文档不需要 token；文档里可以点 Authorize 再调受保护接口。
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/openapi.json" {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tg_down"`)
			writeError(w, http.StatusUnauthorized, "unauthorized", "缺少或错误的 Bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
