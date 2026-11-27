package server

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// zipFS 把内存里的 zip 包成 fs.FS，模拟 webembed 的内嵌产物。
//
// 压缩包里的文件不支持 Seek，正好复现 http.ServeContent 嗅探内容后
// 无法回退到起点的问题。
func zipFS(t *testing.T) fs.FS {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := map[string]string{
		"index.html":        "<html></html>",
		"assets/font.woff2": "wOF2-font-bytes",
		"assets/app.js":     "console.log(1)",
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("打开 zip: %v", err)
	}
	return zr
}

// TestSPAFileServerFontMIME 确认字体文件从内嵌 zip 里也能正常返回。
//
// 字体扩展名不在 Go 内建 MIME 表里时，ServeContent 会嗅探内容再 Seek 回起点，
// 压缩包文件不支持 Seek，结果就是 500 "seeker can't seek"。
func TestSPAFileServerFontMIME(t *testing.T) {
	handler := spaFileServer(zipFS(t))

	req := httptest.NewRequest(http.MethodGet, "/assets/font.woff2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "font/woff2") {
		t.Errorf("Content-Type = %q，期望 font/woff2", ct)
	}
	if body := rec.Body.String(); body != "wOF2-font-bytes" {
		t.Errorf("响应体 = %q", body)
	}
}

// TestSPAFileServerFallback 确认前端 history 路由回退到 index.html，
// 但带扩展名的缺失资源仍然返回 404。
func TestSPAFileServerFallback(t *testing.T) {
	handler := spaFileServer(zipFS(t))

	cases := []struct {
		path string
		want int
	}{
		{"/downloads", http.StatusOK},
		{"/assets/missing.js", http.StatusNotFound},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s 状态码 = %d，期望 %d", tc.path, rec.Code, tc.want)
		}
	}
}
