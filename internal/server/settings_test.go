package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
)

// settingsHandler 返回注入了给定配置回调的处理器。
func settingsHandler(t *testing.T, get func() ([]config.Entry, error), update func(map[string]*string) ([]config.Entry, error)) http.Handler {
	t.Helper()
	cfg := Config{Addr: "127.0.0.1:0"}
	if get != nil {
		cfg.GetSettings = func(_ context.Context) ([]config.Entry, error) { return get() }
	}
	if update != nil {
		cfg.UpdateSettings = func(_ context.Context, patch map[string]*string) ([]config.Entry, error) {
			return update(patch)
		}
	}
	return New(cfg).Handler()
}

// TestSettingsGetMasksAndMarksReadOnly 确认脱敏、只读标记与说明都由本包补齐。
func TestSettingsGetMasksAndMarksReadOnly(t *testing.T) {
	entries := []config.Entry{
		{Key: config.KeyHTTPProxy, Type: config.TypeString, Value: "http://user:secret@127.0.0.1:7897", Known: true, Description: "出站代理"},
		{Key: config.KeyCurrentAccountID, Type: config.TypeInteger, Value: "7", Known: true},
	}
	h := settingsHandler(t, func() ([]config.Entry, error) { return entries, nil }, nil)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}

	var resp SettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Settings) != 2 {
		t.Fatalf("配置项数 = %d，期望 2", len(resp.Settings))
	}

	proxy := resp.Settings[0]
	if proxy.Value != "http://user:***@127.0.0.1:7897" {
		t.Errorf("代理展示值 = %q，应为脱敏后的 ***", proxy.Value)
	}
	if proxy.Description != "出站代理" {
		t.Errorf("description = %q", proxy.Description)
	}
	if !resp.Settings[1].ReadOnly {
		t.Error("current_account_id 应标记为只读")
	}
}

// TestSettingsUpdateRejectsReadOnly 确认只读项在进入业务层前被拒。
func TestSettingsUpdateRejectsReadOnly(t *testing.T) {
	called := false
	h := settingsHandler(t, nil, func(patch map[string]*string) ([]config.Entry, error) {
		called = true
		return nil, nil
	})

	body := `{"current_account_id":"1"}`
	rec := doJSONRequest(t, h, http.MethodPut, "/api/v1/settings", body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
	if called {
		t.Error("只读项不应进入业务层")
	}
}

// TestSettingsUpdateInvalid 确认业务层的非法配置映射为 400。
func TestSettingsUpdateInvalid(t *testing.T) {
	h := settingsHandler(t, nil, func(map[string]*string) ([]config.Entry, error) {
		return nil, config.ErrInvalidSetting
	})

	rec := doJSONRequest(t, h, http.MethodPut, "/api/v1/settings", `{"http_proxy":"x"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

// TestSettingsUpdateBadJSON 确认请求体不是 JSON 时返回 400。
func TestSettingsUpdateBadJSON(t *testing.T) {
	h := settingsHandler(t, nil, func(map[string]*string) ([]config.Entry, error) {
		t.Error("非法 JSON 不应进入业务层")
		return nil, nil
	})

	rec := doJSONRequest(t, h, http.MethodPut, "/api/v1/settings", `{`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

// TestSettingsNotImplemented 未注入回调时返回 501。
func TestSettingsNotImplemented(t *testing.T) {
	h := newTestHandler(t)
	if rec := doRequest(t, h, http.MethodGet, "/api/v1/settings", nil); rec.Code != http.StatusNotImplemented {
		t.Errorf("GET 状态码 = %d，期望 501", rec.Code)
	}
	if rec := doJSONRequest(t, h, http.MethodPut, "/api/v1/settings", `{}`, nil); rec.Code != http.StatusNotImplemented {
		t.Errorf("PUT 状态码 = %d，期望 501", rec.Code)
	}
}
