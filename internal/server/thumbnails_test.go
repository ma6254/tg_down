package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// registerWithData 登记一张缩略图并直接塞入字节，避免测试真的连 Telegram。
func registerWithData(t *testing.T, payload []byte) string {
	t.Helper()
	token := RegisterThumbnail(mtproto.Session{}, mtproto.Thumb{})
	thumbnails.mu.Lock()
	defer thumbnails.mu.Unlock()
	entry, ok := thumbnails.items[token]
	if !ok {
		t.Fatalf("token %q 未登记", token)
	}
	entry.data = payload
	return token
}

// TestMediaThumbnailSuccess 正常返回图片字节与缓存头。
func TestMediaThumbnailSuccess(t *testing.T) {
	payload := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	token := registerWithData(t, payload)

	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/media/thumbnail/"+token, nil)
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

// TestMediaThumbnailUnknownToken 未登记或已过期的 token 返回 404。
func TestMediaThumbnailUnknownToken(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/media/thumbnail/deadbeef", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}

// TestMediaThumbnailExpiredToken 过期条目按未登记处理。
func TestMediaThumbnailExpiredToken(t *testing.T) {
	token := RegisterThumbnail(mtproto.Session{}, mtproto.Thumb{})

	thumbnails.mu.Lock()
	thumbnails.items[token].expires = time.Now().Add(-time.Minute)
	thumbnails.mu.Unlock()

	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/media/thumbnail/"+token, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}

// TestMediaThumbnailInvalidToken 超长 token 直接拒绝。
func TestMediaThumbnailInvalidToken(t *testing.T) {
	long := make([]byte, 80)
	for i := range long {
		long[i] = 'a'
	}
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/media/thumbnail/"+string(long), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

// TestWriteThumbnailError 覆盖缩略图错误的 HTTP 映射。
func TestWriteThumbnailError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{"token 失效", ErrThumbnailNotFound, http.StatusNotFound, "not_found"},
		{"任务不存在", ErrDownloadNotFound, http.StatusNotFound, "not_found"},
		{"下载超时", context.DeadlineExceeded, http.StatusGatewayTimeout, "thumbnail_timeout"},
		{"未选择账号", ErrNoCurrentAccount, http.StatusConflict, "conflict"},
		{"无 session", ErrAccountNoSession, http.StatusConflict, "conflict"},
		{"下载失败", errors.New("boom"), http.StatusBadGateway, "thumbnail_failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeThumbnailError(rec, tt.err)

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
