package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// TestMediaThumbnailNotImplemented 未注入实现时返回 501。
func TestMediaThumbnailNotImplemented(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/media/thumbnail/abc", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d，期望 501", rec.Code)
	}
}

// TestMediaThumbnailSuccess 正常返回图片字节与缓存头。
func TestMediaThumbnailSuccess(t *testing.T) {
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	h := New(Config{
		GetMediaThumbnail: func(_ context.Context, token string) (MediaThumbnail, error) {
			if token != "abc" {
				t.Fatalf("token = %q", token)
			}
			return MediaThumbnail{Data: payload, ContentType: "image/jpeg"}, nil
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/api/v1/media/thumbnail/abc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Error("缺少 Cache-Control")
	}
	if string(rec.Body.Bytes()) != string(payload) {
		t.Errorf("响应体 = %v", rec.Body.Bytes())
	}
}

// TestMediaThumbnailErrors 覆盖 token 失效与下载失败的状态码。
func TestMediaThumbnailErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{"token 失效", ErrThumbnailNotFound, http.StatusNotFound, "not_found"},
		{"下载超时", context.DeadlineExceeded, http.StatusGatewayTimeout, "thumbnail_timeout"},
		{"下载失败", errors.New("boom"), http.StatusBadGateway, "thumbnail_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{
				GetMediaThumbnail: func(context.Context, string) (MediaThumbnail, error) {
					return MediaThumbnail{}, tt.err
				},
			}).Handler()

			rec := doRequest(t, h, http.MethodGet, "/api/v1/media/thumbnail/abc", nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("状态码 = %d，期望 %d", rec.Code, tt.wantCode)
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tt.wantType {
				t.Errorf("错误码 = %q，期望 %q", body.Error.Code, tt.wantType)
			}
		})
	}
}
