package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestAccountLogout 覆盖登出接口的成功响应与错误映射。
func TestAccountLogout(t *testing.T) {
	h := New(Config{
		LogoutAccount: func(_ context.Context, id int64) (AccountLogoutResult, error) {
			switch id {
			case 1:
				return AccountLogoutResult{OK: true, Revoked: true, RemovedSession: true}, nil
			case 2:
				return AccountLogoutResult{}, ErrAccountNotFound
			default:
				return AccountLogoutResult{}, errors.New("boom")
			}
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/1/logout", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"revoked":true`) {
		t.Errorf("body = %s", body)
	}

	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/2/logout", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在账号状态码 = %d，期望 404", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/abc/logout", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 ID 状态码 = %d，期望 400", rec.Code)
	}
}

// TestAccountLogoutNotImplemented 未注入实现时返回 501。
func TestAccountLogoutNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/1/logout", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}
