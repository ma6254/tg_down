package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// TestPreviewLinksNotImplemented 未注入业务实现时返回 501。
func TestPreviewLinksNotImplemented(t *testing.T) {
	rec := doJSONRequest(t, newTestHandler(t), http.MethodPost, "/api/v1/links/preview",
		`{"links":["https://t.me/a/1"]}`, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d，期望 501", rec.Code)
	}
}

// TestPreviewLinksValidation 覆盖请求体与链接列表的校验。
func TestPreviewLinksValidation(t *testing.T) {
	h := New(Config{
		PreviewLinks: func(context.Context, PreviewRequest) (PreviewResponse, error) {
			t.Fatal("校验失败时不应调用业务层")
			return PreviewResponse{}, nil
		},
	}).Handler()

	tooMany := make([]string, maxPreviewLinks+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://t.me/a/%d", i+1)
	}
	tooManyBody, err := json.Marshal(PreviewRequest{Links: tooMany})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
	}{
		{"非法 JSON", `{"links":`},
		{"links 为空", `{"links":[]}`},
		{"links 全是空白", `{"links":["  ",""]}`},
		{"缺少 links", `{}`},
		{"超过数量上限", string(tooManyBody)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/links/preview", tt.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400", rec.Code)
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("解析错误响应: %v", err)
			}
			if body.Error.Code != "bad_request" {
				t.Errorf("错误码 = %q，期望 bad_request", body.Error.Code)
			}
		})
	}
}

// TestPreviewLinksSuccess 覆盖正常响应与请求归一化。
func TestPreviewLinksSuccess(t *testing.T) {
	var got PreviewRequest
	h := New(Config{
		PreviewLinks: func(_ context.Context, req PreviewRequest) (PreviewResponse, error) {
			got = req
			return PreviewResponse{
				Items: []PreviewItem{{
					Link:      "https://t.me/a/1",
					MessageID: 1,
					Chat:      &PreviewChat{Kind: "channel", ID: 1, Title: "频道"},
					Text:      "第 1 集",
				}},
				Summary: PreviewSummary{Total: 1, OK: 1},
			}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/links/preview",
		`{"links":["  https://t.me/a/1  ","","https://t.me/a/2"],"expand_text_links":true}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}

	want := []string{"https://t.me/a/1", "https://t.me/a/2"}
	if !reflect.DeepEqual(got.Links, want) {
		t.Errorf("业务层收到 links = %#v，期望 %#v", got.Links, want)
	}
	if !got.ExpandTextLinks {
		t.Error("expand_text_links 没有传给业务层")
	}

	var resp PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Chat == nil || resp.Items[0].Chat.Title != "频道" {
		t.Errorf("响应 items 不正确: %+v", resp.Items)
	}
}

// TestPreviewLinksEmptyItems 确认空结果返回 [] 而不是 null。
func TestPreviewLinksEmptyItems(t *testing.T) {
	h := New(Config{
		PreviewLinks: func(context.Context, PreviewRequest) (PreviewResponse, error) {
			return PreviewResponse{}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/links/preview",
		`{"links":["https://t.me/a/1"]}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var raw struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Items) != "[]" {
		t.Errorf("items = %s，期望 []", raw.Items)
	}
}

// TestPreviewLinksErrors 覆盖业务错误到 HTTP 状态码的映射。
func TestPreviewLinksErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{"没有当前账号", ErrNoCurrentAccount, http.StatusConflict, "conflict"},
		{"session 不可用", ErrAccountNoSession, http.StatusConflict, "conflict"},
		{"超时", context.DeadlineExceeded, http.StatusGatewayTimeout, "preview_timeout"},
		{"联网失败", ErrPreviewFailed, http.StatusBadGateway, "preview_failed"},
		{"其他错误", errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{
				PreviewLinks: func(context.Context, PreviewRequest) (PreviewResponse, error) {
					return PreviewResponse{}, tt.err
				},
			}).Handler()

			rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/links/preview",
				`{"links":["https://t.me/a/1"]}`, nil)
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
