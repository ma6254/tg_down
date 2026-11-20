package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestAccountDelete 覆盖删除账号接口的成功与错误映射。
func TestAccountDelete(t *testing.T) {
	h := New(Config{
		DeleteAccount: func(_ context.Context, id int64) (AccountDeleteResult, error) {
			if id == 1 {
				return AccountDeleteResult{OK: true, RemovedSession: true}, nil
			}
			return AccountDeleteResult{}, ErrAccountNotFound
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodDelete, "/api/v1/accounts/1", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed_session":true`) {
		t.Fatalf("状态码 = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = doJSONRequest(t, h, http.MethodDelete, "/api/v1/accounts/2", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在账号状态码 = %d，期望 404", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodDelete, "/api/v1/accounts/abc", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 ID 状态码 = %d，期望 400", rec.Code)
	}
}

// TestAccountDeleteNotImplemented 未注入实现时返回 501。
func TestAccountDeleteNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doJSONRequest(t, h, http.MethodDelete, "/api/v1/accounts/1", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}
