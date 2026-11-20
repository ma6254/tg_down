package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/build"
)

// newTestHandler 返回默认配置（占位页、免鉴权）的 handler。
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return New(Config{Addr: "127.0.0.1:0"}).Handler()
}

func doRequest(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doJSONRequest 发送带 JSON body 的请求，用于 POST 类接口。
func doJSONRequest(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/health", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q，期望 application/json", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cc)
	}

	var body struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		UptimeSeconds int64  `json:"uptime_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q，期望 ok", body.Status)
	}
	if body.Version == "" {
		t.Error("响应缺少 version 字段")
	}
}

func TestAuth(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		path   string
		header string
		want   int
	}{
		{name: "未配置 token 时直接放行", token: "", path: "/api/v1/health", want: http.StatusOK},
		{name: "缺少 Authorization", token: "s3cret", path: "/api/v1/health", want: http.StatusUnauthorized},
		{name: "token 错误", token: "s3cret", path: "/api/v1/health", header: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "token 正确", token: "s3cret", path: "/api/v1/health", header: "Bearer s3cret", want: http.StatusOK},
		{name: "静态页面不需要 token", token: "s3cret", path: "/", want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{Token: tt.token}).Handler()
			headers := map[string]string{}
			if tt.header != "" {
				headers["Authorization"] = tt.header
			}
			rec := doRequest(t, h, http.MethodGet, tt.path, headers)
			if rec.Code != tt.want {
				t.Fatalf("状态码 = %d，期望 %d", rec.Code, tt.want)
			}
		})
	}
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "首页返回占位页", method: http.MethodGet, path: "/", want: http.StatusOK},
		{name: "未知页面 404", method: http.MethodGet, path: "/nope", want: http.StatusNotFound},
		{name: "未实现接口 404", method: http.MethodGet, path: "/api/v1/nope", want: http.StatusNotFound},
		// 兜底模式不带方法，方法不匹配的请求也会落到 404 而不是 405。
		{name: "方法不匹配由 /api/ 兜底 404", method: http.MethodPost, path: "/api/v1/health", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, newTestHandler(t), tt.method, tt.path, nil)
			if rec.Code != tt.want {
				t.Fatalf("状态码 = %d，期望 %d", rec.Code, tt.want)
			}
		})
	}
}

func TestUnknownAPIErrorShape(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/nope", nil)

	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Errorf("error.code = %q，期望 not_found", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("error.message 为空")
	}
}

func TestLoginProbe(t *testing.T) {
	t.Run("成功返回探测结果", func(t *testing.T) {
		var got ProbeRequest
		h := New(Config{ProbeLogin: func(_ context.Context, req ProbeRequest) (ProbeResponse, error) {
			got = req
			return ProbeResponse{
				TDataPath: `C:\tdata`,
				Accounts: []ProbeAccount{{
					Index:    1,
					UserID:   42,
					Name:     "张三",
					Username: "zhangsan",
					Phone:    "+8613800000000",
					Devices:  []ProbeDevice{{Current: true, AppName: "Telegram Desktop", Platform: "Windows"}},
				}},
				Imported:          1,
				Skipped:           0,
				SelectedAccountID: 7,
			}, nil
		}}).Handler()

		rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/probe",
			`{"tdata":"C:\\tdata","tdata_passcode":"1234","timeout_seconds":10}`, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
		}

		var body ProbeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if len(body.Accounts) != 1 || body.Accounts[0].Name != "张三" {
			t.Fatalf("accounts = %+v", body.Accounts)
		}
		if body.Imported != 1 || body.SelectedAccountID != 7 {
			t.Errorf("imported = %d，selected = %d", body.Imported, body.SelectedAccountID)
		}
		if got.TDataPath != `C:\tdata` || got.TDataPasscode != "1234" || got.TimeoutSecond != 10 {
			t.Errorf("业务层收到的请求 = %+v", got)
		}
		if got.Import != nil {
			t.Error("请求未指定 import 时不应被改写")
		}
	})

	t.Run("空请求体按默认值处理", func(t *testing.T) {
		called := false
		h := New(Config{ProbeLogin: func(_ context.Context, req ProbeRequest) (ProbeResponse, error) {
			called = true
			if req.TDataPath != "" || req.TimeoutSecond != 0 || req.Import != nil {
				t.Errorf("默认请求 = %+v", req)
			}
			return ProbeResponse{Accounts: []ProbeAccount{}}, nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", nil)
		if rec.Code != http.StatusOK || !called {
			t.Fatalf("状态码 = %d，called = %v", rec.Code, called)
		}
	})

	t.Run("未接入时返回 501", func(t *testing.T) {
		rec := doRequest(t, newTestHandler(t), http.MethodPost, "/api/v1/accounts/probe", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("状态码 = %d，期望 501", rec.Code)
		}
	})

	t.Run("非法 JSON 返回 400", func(t *testing.T) {
		h := New(Config{ProbeLogin: func(context.Context, ProbeRequest) (ProbeResponse, error) {
			t.Fatal("非法 JSON 不应进入业务层")
			return ProbeResponse{}, nil
		}}).Handler()
		rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", `{"tdata":`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400", rec.Code)
		}
	})

	t.Run("超时超出范围返回 400", func(t *testing.T) {
		h := New(Config{ProbeLogin: func(context.Context, ProbeRequest) (ProbeResponse, error) {
			t.Fatal("非法超时不应进入业务层")
			return ProbeResponse{}, nil
		}}).Handler()
		rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", `{"timeout_seconds":1}`, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400", rec.Code)
		}
	})

	t.Run("探测超时返回 504", func(t *testing.T) {
		h := New(Config{ProbeLogin: func(context.Context, ProbeRequest) (ProbeResponse, error) {
			return ProbeResponse{}, fmt.Errorf("连接 Telegram: %w", context.DeadlineExceeded)
		}}).Handler()
		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", nil)
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("状态码 = %d，期望 504", rec.Code)
		}
	})

	t.Run("探测失败返回 400 并带上原因", func(t *testing.T) {
		h := New(Config{ProbeLogin: func(context.Context, ProbeRequest) (ProbeResponse, error) {
			return ProbeResponse{}, errors.New("未找到 Telegram Desktop 的 tdata 目录")
		}}).Handler()
		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400", rec.Code)
		}
		var body ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if body.Error.Code != "probe_failed" || !strings.Contains(body.Error.Message, "tdata") {
			t.Errorf("error = %+v", body.Error)
		}
	})

	t.Run("需要 token", func(t *testing.T) {
		h := New(Config{
			Token: "s3cret",
			ProbeLogin: func(context.Context, ProbeRequest) (ProbeResponse, error) {
				return ProbeResponse{Accounts: []ProbeAccount{}}, nil
			},
		}).Handler()
		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/probe", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
	})
}

func TestAccountList(t *testing.T) {
	t.Run("返回账号与当前标记", func(t *testing.T) {
		h := New(Config{ListAccounts: func(context.Context) ([]Account, error) {
			return []Account{
				{ID: 2, Kind: "mtproto", Name: "B", State: "authed", CreatedAt: 1700000000},
				{ID: 1, Kind: "botapi", Name: "A", State: "error", LastError: "token 失效", CreatedAt: 1690000000, Current: true},
			}, nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
		}
		var body AccountsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if len(body.Accounts) != 2 {
			t.Fatalf("accounts = %+v", body.Accounts)
		}
		if body.CurrentAccountID != 1 {
			t.Errorf("current_account_id = %d，期望 1", body.CurrentAccountID)
		}
		if body.Accounts[0].Name != "B" || body.Accounts[1].LastError != "token 失效" {
			t.Errorf("账号字段 = %+v", body.Accounts)
		}
	})

	t.Run("空列表返回数组而不是 null", func(t *testing.T) {
		h := New(Config{ListAccounts: func(context.Context) ([]Account, error) {
			return nil, nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), `"accounts":null`) {
			t.Fatalf("accounts 不应为 null: %s", rec.Body.String())
		}
		var body AccountsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if body.Accounts == nil || len(body.Accounts) != 0 {
			t.Errorf("accounts = %+v", body.Accounts)
		}
	})

	t.Run("未接入返回 501", func(t *testing.T) {
		rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/accounts", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("状态码 = %d，期望 501", rec.Code)
		}
	})

	t.Run("数据库错误返回 500", func(t *testing.T) {
		h := New(Config{ListAccounts: func(context.Context) ([]Account, error) {
			return nil, errors.New("database is locked")
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "internal_error" {
			t.Errorf("error.code = %q，期望 internal_error", body.Error.Code)
		}
	})

	t.Run("需要 token", func(t *testing.T) {
		h := New(Config{
			Token: "s3cret",
			ListAccounts: func(context.Context) ([]Account, error) {
				return []Account{}, nil
			},
		}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
	})
}

func TestAccountSelect(t *testing.T) {
	t.Run("切换成功", func(t *testing.T) {
		var gotID int64
		h := New(Config{SelectAccount: func(_ context.Context, id int64) error {
			gotID = id
			return nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/3/select", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
		}
		var body SelectAccountResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if !body.OK || body.CurrentAccountID != 3 {
			t.Errorf("响应 = %+v", body)
		}
		if gotID != 3 {
			t.Errorf("业务层收到 id = %d，期望 3", gotID)
		}
	})

	t.Run("非法 ID 返回 400", func(t *testing.T) {
		h := New(Config{SelectAccount: func(context.Context, int64) error {
			t.Fatal("非法 ID 不应进入业务层")
			return nil
		}}).Handler()

		for _, path := range []string{"/api/v1/accounts/abc/select", "/api/v1/accounts/0/select"} {
			rec := doRequest(t, h, http.MethodPost, path, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
			}
		}
	})

	t.Run("账号不存在返回 404", func(t *testing.T) {
		h := New(Config{SelectAccount: func(context.Context, int64) error {
			return fmt.Errorf("%w: 账号 ID 9 不存在", ErrAccountNotFound)
		}}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/9/select", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "not_found" {
			t.Errorf("error.code = %q，期望 not_found", body.Error.Code)
		}
	})

	t.Run("session 缺失返回 409", func(t *testing.T) {
		h := New(Config{SelectAccount: func(context.Context, int64) error {
			return fmt.Errorf("%w: 账号 #2 的 session 文件不存在", ErrAccountNoSession)
		}}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/2/select", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("状态码 = %d，期望 409", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "conflict" {
			t.Errorf("error.code = %q，期望 conflict", body.Error.Code)
		}
	})

	t.Run("内部错误返回 500", func(t *testing.T) {
		h := New(Config{SelectAccount: func(context.Context, int64) error {
			return errors.New("database is locked")
		}}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/1/select", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", rec.Code)
		}
	})

	t.Run("未接入返回 501", func(t *testing.T) {
		rec := doRequest(t, newTestHandler(t), http.MethodPost, "/api/v1/accounts/1/select", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("状态码 = %d，期望 501", rec.Code)
		}
	})

	t.Run("需要 token", func(t *testing.T) {
		h := New(Config{
			Token: "s3cret",
			SelectAccount: func(context.Context, int64) error {
				return nil
			},
		}).Handler()

		rec := doRequest(t, h, http.MethodPost, "/api/v1/accounts/1/select", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
	})
}

func TestAccountGet(t *testing.T) {
	t.Run("返回账号详情", func(t *testing.T) {
		var gotID int64
		h := New(Config{GetAccount: func(_ context.Context, id int64) (AccountDetail, error) {
			gotID = id
			return AccountDetail{
				Account: Account{
					ID: 3, Kind: "mtproto", Name: "张三", Username: "zhangsan",
					Phone: "+8613800000000", State: "authed",
					CreatedAt: 1700000000, Current: true,
				},
				UpdatedAt:      1700000100,
				SessionPresent: true,
			}, nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/3", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"session_present":true`) {
			t.Errorf("响应缺少 session_present: %s", rec.Body.String())
		}

		var body AccountDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if body.ID != 3 || body.Name != "张三" || !body.Current || !body.SessionPresent {
			t.Errorf("详情 = %+v", body)
		}
		if body.UpdatedAt != 1700000100 {
			t.Errorf("updated_at = %d", body.UpdatedAt)
		}
		if gotID != 3 {
			t.Errorf("业务层收到 id = %d，期望 3", gotID)
		}
	})

	t.Run("非法 ID 返回 400", func(t *testing.T) {
		h := New(Config{GetAccount: func(context.Context, int64) (AccountDetail, error) {
			t.Fatal("非法 ID 不应进入业务层")
			return AccountDetail{}, nil
		}}).Handler()

		for _, path := range []string{"/api/v1/accounts/abc", "/api/v1/accounts/0"} {
			rec := doRequest(t, h, http.MethodGet, path, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
			}
		}
	})

	t.Run("账号不存在返回 404", func(t *testing.T) {
		h := New(Config{GetAccount: func(context.Context, int64) (AccountDetail, error) {
			return AccountDetail{}, fmt.Errorf("%w: 账号 ID 9 不存在", ErrAccountNotFound)
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/9", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", rec.Code)
		}
	})

	t.Run("内部错误返回 500", func(t *testing.T) {
		h := New(Config{GetAccount: func(context.Context, int64) (AccountDetail, error) {
			return AccountDetail{}, errors.New("database is locked")
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", rec.Code)
		}
	})

	t.Run("未接入返回 501", func(t *testing.T) {
		rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/accounts/1", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("状态码 = %d，期望 501", rec.Code)
		}
	})

	t.Run("需要 token", func(t *testing.T) {
		h := New(Config{
			Token: "s3cret",
			GetAccount: func(context.Context, int64) (AccountDetail, error) {
				return AccountDetail{}, nil
			},
		}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
	})
}

func TestAccountLive(t *testing.T) {
	t.Run("返回在线资料", func(t *testing.T) {
		var gotID int64
		h := New(Config{GetAccountLive: func(_ context.Context, id int64) (AccountLiveInfo, error) {
			gotID = id
			return AccountLiveInfo{
				UserID:   42,
				Name:     "张三",
				Username: "zhangsan",
				Phone:    "8613800000000",
				Premium:  true,
				About:    "hi",
				Birthday: &AccountBirthday{Day: 2, Month: 3, Year: 2000},
				Devices: []ProbeDevice{{
					Current: true, AppName: "Telegram Desktop", Platform: "Windows",
				}},
				FetchedAt: 1700000000,
			}, nil
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/3/live", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，body = %s", rec.Code, rec.Body.String())
		}

		var body AccountLiveInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析 JSON: %v", err)
		}
		if body.UserID != 42 || body.Name != "张三" || !body.Premium || body.About != "hi" {
			t.Errorf("在线资料 = %+v", body)
		}
		if body.Birthday == nil || body.Birthday.Day != 2 || body.Birthday.Month != 3 || body.Birthday.Year != 2000 {
			t.Errorf("birthday = %+v", body.Birthday)
		}
		if len(body.Devices) != 1 || !body.Devices[0].Current {
			t.Errorf("devices = %+v", body.Devices)
		}
		if body.FetchedAt != 1700000000 {
			t.Errorf("fetched_at = %d", body.FetchedAt)
		}
		if gotID != 3 {
			t.Errorf("业务层收到 id = %d，期望 3", gotID)
		}
	})

	t.Run("非法 ID 返回 400", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			t.Fatal("非法 ID 不应进入业务层")
			return AccountLiveInfo{}, nil
		}}).Handler()

		for _, path := range []string{"/api/v1/accounts/abc/live", "/api/v1/accounts/0/live"} {
			rec := doRequest(t, h, http.MethodGet, path, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
			}
		}
	})

	t.Run("账号不存在返回 404", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			return AccountLiveInfo{}, fmt.Errorf("%w: 账号 ID 9 不存在", ErrAccountNotFound)
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/9/live", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", rec.Code)
		}
	})

	t.Run("session 缺失返回 409", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			return AccountLiveInfo{}, fmt.Errorf("%w: 账号 #2 的 session 文件不存在", ErrAccountNoSession)
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/2/live", nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("状态码 = %d，期望 409", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "conflict" {
			t.Errorf("error.code = %q，期望 conflict", body.Error.Code)
		}
	})

	t.Run("联网失败返回 502", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			return AccountLiveInfo{}, fmt.Errorf("%w: session 已失效", ErrAccountLiveFailed)
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1/live", nil)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("状态码 = %d，期望 502", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "live_failed" {
			t.Errorf("error.code = %q，期望 live_failed", body.Error.Code)
		}
	})

	t.Run("查询超时返回 504", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			return AccountLiveInfo{}, fmt.Errorf("查询账号资料: %w", context.DeadlineExceeded)
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1/live", nil)
		if rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("状态码 = %d，期望 504", rec.Code)
		}
		var body ErrorResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body.Error.Code != "live_timeout" {
			t.Errorf("error.code = %q，期望 live_timeout", body.Error.Code)
		}
	})

	t.Run("内部错误返回 500", func(t *testing.T) {
		h := New(Config{GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
			return AccountLiveInfo{}, errors.New("database is locked")
		}}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1/live", nil)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("状态码 = %d，期望 500", rec.Code)
		}
	})

	t.Run("未接入返回 501", func(t *testing.T) {
		rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/v1/accounts/1/live", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("状态码 = %d，期望 501", rec.Code)
		}
	})

	t.Run("需要 token", func(t *testing.T) {
		h := New(Config{
			Token: "s3cret",
			GetAccountLive: func(context.Context, int64) (AccountLiveInfo, error) {
				return AccountLiveInfo{}, nil
			},
		}).Handler()

		rec := doRequest(t, h, http.MethodGet, "/api/v1/accounts/1/live", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d，期望 401", rec.Code)
		}
	})
}

func TestOpenAPISpec(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet, "/api/openapi.json", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q，期望 application/json", ct)
	}

	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("规范不是合法 JSON: %v", err)
	}
	if doc.OpenAPI != "3.0.3" {
		t.Errorf("openapi = %q，期望 3.0.3", doc.OpenAPI)
	}
	if doc.Info.Version != build.Version {
		t.Errorf("info.version = %q，期望替换为 %q", doc.Info.Version, build.Version)
	}
	if _, ok := doc.Paths["/api/v1/health"]; !ok {
		t.Errorf("规范缺少 /api/v1/health")
	}
	if _, ok := doc.Paths["/api/v1/accounts/{id}/live"]; !ok {
		t.Errorf("规范缺少 /api/v1/accounts/{id}/live")
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(versionPlaceholder)) {
		t.Error("规范里的版本占位符没有被替换")
	}
}

func TestSwaggerUI(t *testing.T) {
	h := newTestHandler(t)

	t.Run("首页", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/swagger/", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "swagger-ui") {
			t.Errorf("body 不含 swagger-ui，前 80 字节: %q", rec.Body.String()[:min(80, rec.Body.Len())])
		}
	})

	t.Run("initializer 指向本服务规范", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/swagger/swagger-initializer.js", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "/api/openapi.json") {
			t.Error("initializer 没有指向 /api/openapi.json")
		}
		if !strings.Contains(rec.Body.String(), "persistAuthorization") {
			t.Error("initializer 缺少 persistAuthorization，刷新页面会丢失 Authorize 的 token")
		}
	})

	t.Run("静态资源可从内嵌 FS 读取", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/swagger/swagger-ui.css", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
	})

	t.Run("缺少末尾斜杠时重定向", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/swagger", nil)
		if rec.Code != http.StatusMovedPermanently {
			t.Fatalf("状态码 = %d，期望 301", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/swagger/" {
			t.Errorf("Location = %q，期望 /swagger/", loc)
		}
	})
}

func TestDocsArePublic(t *testing.T) {
	h := New(Config{Token: "s3cret"}).Handler()

	if rec := doRequest(t, h, http.MethodGet, "/api/openapi.json", nil); rec.Code != http.StatusOK {
		t.Errorf("/api/openapi.json 状态码 = %d，期望 200", rec.Code)
	}
	if rec := doRequest(t, h, http.MethodGet, "/swagger/", nil); rec.Code != http.StatusOK {
		t.Errorf("/swagger/ 状态码 = %d，期望 200", rec.Code)
	}
	// 受保护接口仍然要求 token。
	if rec := doRequest(t, h, http.MethodGet, "/api/v1/health", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/v1/health 状态码 = %d，期望 401", rec.Code)
	}
}

func TestSPADirMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>spa-index</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.abc123.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(Config{Web: WebConfig{Mode: "dir", Dir: dir}}).Handler()

	t.Run("前端路由回退 index.html", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/tool/json", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "spa-index") {
			t.Errorf("body = %q，期望 index.html 内容", rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("Cache-Control = %q，期望 no-cache", cc)
		}
	})

	t.Run("带哈希的静态资源长缓存", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/assets/app.abc123.js", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("Cache-Control = %q，期望包含 immutable", cc)
		}
	})

	t.Run("缺失的静态资源不回退", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/assets/missing.js", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d，期望 404", rec.Code)
		}
	})
}

func TestWebOffMode(t *testing.T) {
	h := New(Config{Web: WebConfig{Mode: "off"}}).Handler()

	if rec := doRequest(t, h, http.MethodGet, "/", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/ 状态码 = %d，期望 404", rec.Code)
	}
	if rec := doRequest(t, h, http.MethodGet, "/api/v1/health", nil); rec.Code != http.StatusOK {
		t.Errorf("/api/v1/health 状态码 = %d，期望 200", rec.Code)
	}
}

func TestWebProxyMode(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "vite dev: "+r.URL.Path)
	}))
	defer backend.Close()

	h := New(Config{Web: WebConfig{Mode: "proxy", ProxyURL: backend.URL}}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "vite dev: /") {
		t.Errorf("body = %q，期望反向代理到后端", rec.Body.String())
	}
}

func TestServeAndStop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("侦听测试端口: %v", err)
	}
	srv := New(Config{Addr: ln.Addr().String()})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	// 轮询健康检查，确认服务真的起来了。
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("服务未就绪: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Serve 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve 未退出")
	}
}

func TestRunListenError(t *testing.T) {
	srv := New(Config{Addr: "127.0.0.1:70000"})
	if err := srv.Run(); err == nil {
		t.Fatal("非法端口应返回错误")
	}
}

// TestStopClosesSSE 确认 Stop 会取消长连接，而不是干等到超时。
//
// /api/v1/events 是常驻 SSE：Shutdown 不会取消 handler 的 context，
// 只等待它返回；不做 BaseContext 取消的话 Ctrl+C 必然拖满 10s 再报错。
func TestStopClosesSSE(t *testing.T) {
	hub := NewEventHub()
	srv := New(Config{EventHub: hub})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/api/v1/events")
	if err != nil {
		t.Fatalf("连接 SSE 失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	// 读一帧 ping，确保 handler 已经进入事件循环。
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("读取 SSE 帧失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop 超时或失败: %v", err)
	}

	_ = ln.Close()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve 返回错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 未随 Stop 退出")
	}
}
