package server

import (
	"context"
	"net/http"
	"testing"
)

// TestDownloadThumbnail 覆盖任务缩略图接口的状态码与响应头。
func TestDownloadThumbnail(t *testing.T) {
	h := New(Config{
		GetDownloadThumbnail: func(_ context.Context, id uint) (MediaThumbnail, error) {
			if id == 1 {
				return MediaThumbnail{Data: []byte("jpeg-bytes"), ContentType: "image/jpeg"}, nil
			}
			return MediaThumbnail{}, ErrDownloadNotFound
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/1/thumbnail", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Body.String() != "jpeg-bytes" {
		t.Errorf("body = %q", rec.Body.String())
	}

	rec = doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/2/thumbnail", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在任务状态码 = %d，期望 404", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/abc/thumbnail", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 ID 状态码 = %d，期望 400", rec.Code)
	}
}

// TestDownloadThumbnailNotImplemented 未注入实现时返回 501。
func TestDownloadThumbnailNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/1/thumbnail", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}
