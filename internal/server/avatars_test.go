package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// TestAccountAvatar 覆盖头像接口的正常响应与错误映射。
func TestAccountAvatar(t *testing.T) {
	h := New(Config{
		GetAccountAvatar: func(_ context.Context, id int64) (MediaThumbnail, error) {
			switch id {
			case 1:
				return MediaThumbnail{Data: []byte("jpeg"), ContentType: "image/jpeg"}, nil
			case 2:
				return MediaThumbnail{}, ErrThumbnailNotFound
			case 3:
				return MediaThumbnail{}, ErrAccountNoSession
			default:
				return MediaThumbnail{}, errors.New("boom")
			}
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/accounts/1/avatar", "", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "jpeg" {
		t.Fatalf("状态码 = %d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}

	cases := []struct {
		path string
		want int
	}{
		{"/api/v1/accounts/2/avatar", http.StatusNotFound},
		{"/api/v1/accounts/3/avatar", http.StatusConflict},
		{"/api/v1/accounts/4/avatar", http.StatusInternalServerError},
		{"/api/v1/accounts/abc/avatar", http.StatusBadRequest},
	}
	for _, tt := range cases {
		rec := doJSONRequest(t, h, http.MethodGet, tt.path, "", nil)
		if rec.Code != tt.want {
			t.Errorf("%s 状态码 = %d，期望 %d", tt.path, rec.Code, tt.want)
		}
	}
}

// TestAccountAvatarNotImplemented 未注入实现时返回 501。
func TestAccountAvatarNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/accounts/1/avatar", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}
