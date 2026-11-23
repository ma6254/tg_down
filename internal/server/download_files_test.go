package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// staticReadSeekCloser 是 handler 测试用的内存文件。
type staticReadSeekCloser struct{ *strings.Reader }

func (staticReadSeekCloser) Close() error { return nil }

// newDownloadFileHandler 返回注入了固定文件读取实现的路由。
func newDownloadFileHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(Config{
		GetDownloadFile: func(_ context.Context, id uint) (DownloadFile, error) {
			switch id {
			case 1:
				return DownloadFile{
					Content: staticReadSeekCloser{Reader: strings.NewReader("hello media")},
					Name:    "clip.mp4",
					MIME:    "video/mp4",
					Size:    11,
					ModTime: time.Unix(1700000000, 0),
				}, nil
			case 2:
				return DownloadFile{}, ErrDownloadFileNotFound
			case 3:
				return DownloadFile{}, fmt.Errorf("%w: 任务尚未下载完成", ErrDownloadConflict)
			case 5:
				return DownloadFile{
					Content: staticReadSeekCloser{Reader: strings.NewReader("zip-bytes")},
					Name:    "archive.zip",
					MIME:    "application/zip",
					Size:    9,
				}, nil
			default:
				return DownloadFile{}, ErrDownloadNotFound
			}
		},
	}).Handler()
}

// TestDownloadFile 覆盖普通 GET、HEAD 与 Range。
func TestDownloadFile(t *testing.T) {
	h := newDownloadFileHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/1/file", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if body := rec.Body.String(); body != "hello media" {
		t.Errorf("body = %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Errorf("Content-Disposition = %q，期望 inline", cd)
	}
	if ar := rec.Header().Get("Accept-Ranges"); ar != "bytes" {
		t.Errorf("Accept-Ranges = %q", ar)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}

	rec = doRequest(t, h, http.MethodHead, "/api/v1/downloads/1/file", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD 状态码 = %d，期望 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD 不应有 body，实际 %q", rec.Body.String())
	}
	if cl := rec.Header().Get("Content-Length"); cl != "11" {
		t.Errorf("HEAD Content-Length = %q，期望 11", cl)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/downloads/1/file", map[string]string{
		"Range": "bytes=0-4",
	})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 状态码 = %d，期望 206", rec.Code)
	}
	if body := rec.Body.String(); body != "hello" {
		t.Errorf("Range body = %q，期望 hello", body)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-4/11" {
		t.Errorf("Content-Range = %q", cr)
	}
}

// TestDownloadFileErrors 覆盖 404/409/400 与附件下载。
func TestDownloadFileErrors(t *testing.T) {
	h := newDownloadFileHandler(t)

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"文件不存在", "/api/v1/downloads/2/file", http.StatusNotFound},
		{"任务未完成", "/api/v1/downloads/3/file", http.StatusConflict},
		{"任务不存在", "/api/v1/downloads/4/file", http.StatusNotFound},
		{"非法 ID", "/api/v1/downloads/abc/file", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, h, http.MethodGet, tc.target, nil)
			if rec.Code != tc.want {
				t.Errorf("状态码 = %d，期望 %d", rec.Code, tc.want)
			}
		})
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/5/file", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("附件状态码 = %d，期望 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q，期望 attachment", cd)
	}
}

// TestDownloadFileNotImplemented 未注入实现时返回 501。
func TestDownloadFileNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/1/file", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}
