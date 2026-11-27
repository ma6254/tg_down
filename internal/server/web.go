package server

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
)

// 字体扩展名不在 Go 内建的 MIME 表里，必须显式登记。
//
// http.ServeContent 遇到未知扩展名会嗅探前 512 字节再 Seek 回起点，
// 而压缩包内嵌的文件不支持 Seek，结果是 500 "seeker can't seek"。
func init() {
	knownTypes := map[string]string{
		".woff2": "font/woff2",
		".woff":  "font/woff",
		".ttf":   "font/ttf",
		".otf":   "font/otf",
	}
	for ext, typ := range knownTypes {
		_ = mime.AddExtensionType(ext, typ)
	}
}

// mountWeb 根据 web.mode 决定前端如何接入。
//
//   - off：不提供前端，只保留 /api
//   - proxy：反向代理到外部前端服务（开发时指向 Vite dev server）
//   - dir：从本地目录提供静态文件
//   - FS：使用内嵌进二进制的前端产物（默认构建）
//   - 都没有：内置占位页
func (s *Server) mountWeb(mux *http.ServeMux) {
	switch s.cfg.Web.Mode {
	case "off":
		return
	case "proxy":
		target, err := url.Parse(s.cfg.Web.ProxyURL)
		if err != nil {
			mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "web proxy url: "+err.Error(), http.StatusInternalServerError)
			}))
			return
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "前端开发服务器不可用："+err.Error(), http.StatusBadGateway)
		}
		mux.Handle("/", proxy)
	case "dir":
		mux.Handle("/", spaFileServer(os.DirFS(s.cfg.Web.Dir)))
	default:
		if s.cfg.Web.FS != nil {
			mux.Handle("/", spaFileServer(s.cfg.Web.FS))
			return
		}
		mux.Handle("/", placeholderPage())
	}
}

// placeholderPage 在前端尚未构建进二进制时提供一张最小页面。
func placeholderPage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, placeholderHTML)
	})
}

const placeholderHTML = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>tg_down</title></head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;line-height:1.6">
<h1>tg_down 服务已启动</h1>
<p>Web 界面尚未构建进二进制。API 已可用：</p>
<ul>
  <li><a href="/api/v1/health">GET /api/v1/health</a> — 健康检查</li>
</ul>
</body>
</html>
`

// spaFileServer 提供静态文件，并为前端 history 路由回退到 index.html。
// 带扩展名但不存在资源返回 404，避免把 HTML 当作 JS/CSS 返回。
func spaFileServer(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		upath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if upath == "" {
			upath = "index.html"
		}
		if info, err := fs.Stat(fsys, upath); err == nil && !info.IsDir() {
			setStaticCache(w, upath)
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.Contains(path.Base(upath), ".") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		fileServer.ServeHTTP(w, clone)
	})
}

// setStaticCache 对带哈希的构建产物启用长缓存，其余文件不缓存。
func setStaticCache(w http.ResponseWriter, name string) {
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
}
