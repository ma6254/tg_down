package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// TestCommentsNotImplemented 未注入业务实现时返回 501。
func TestCommentsNotImplemented(t *testing.T) {
	rec := doRequest(t, newTestHandler(t), http.MethodGet,
		"/api/v1/links/comments?link=https://t.me/a/1", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d，期望 501", rec.Code)
	}
}

// TestCommentsValidation 覆盖 link / limit / offset_id 的参数校验。
func TestCommentsValidation(t *testing.T) {
	h := New(Config{
		GetComments: func(context.Context, CommentsQuery) (CommentsResponse, error) {
			t.Fatal("参数校验失败时不应调用业务层")
			return CommentsResponse{}, nil
		},
	}).Handler()

	tests := []string{
		"/api/v1/links/comments",
		"/api/v1/links/comments?link=",
		"/api/v1/links/comments?link=https://t.me/a/1&limit=0",
		"/api/v1/links/comments?link=https://t.me/a/1&limit=abc",
		"/api/v1/links/comments?link=https://t.me/a/1&offset_id=-1",
		"/api/v1/links/comments?link=https://t.me/a/1&offset_id=abc",
	}
	for _, target := range tests {
		t.Run(target, func(t *testing.T) {
			rec := doRequest(t, h, http.MethodGet, target, nil)
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

// TestCommentsSuccess 覆盖参数传递、limit 截断与响应写出。
func TestCommentsSuccess(t *testing.T) {
	var got CommentsQuery
	h := New(Config{
		GetComments: func(_ context.Context, query CommentsQuery) (CommentsResponse, error) {
			got = query
			return CommentsResponse{
				Link:      "https://t.me/a/1",
				MessageID: 1,
				Available: true,
				Total:     1,
				Comments: []Comment{{
					ID:   9,
					Date: 1700000000,
					Text: "hello",
				}},
				NextOffsetID: 9,
			}, nil
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet,
		"/api/v1/links/comments?link=https://t.me/a/1&limit=999&offset_id=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if got.Link != "https://t.me/a/1" || got.Limit != maxCommentsLimit || got.OffsetID != 5 {
		t.Errorf("业务层收到 query = %+v", got)
	}

	var resp CommentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !resp.Available || resp.Total != 1 || len(resp.Comments) != 1 {
		t.Errorf("响应 = %+v", resp)
	}
}

// TestCommentsEmptyList 确认没有评论时返回 [] 而不是 null。
func TestCommentsEmptyList(t *testing.T) {
	h := New(Config{
		GetComments: func(context.Context, CommentsQuery) (CommentsResponse, error) {
			return CommentsResponse{Available: true}, nil
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet,
		"/api/v1/links/comments?link=https://t.me/a/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var raw struct {
		Comments json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Comments) != "[]" {
		t.Errorf("comments = %s，期望 []", raw.Comments)
	}
}

// TestCommentsErrors 覆盖业务错误到 HTTP 状态码的映射。
func TestCommentsErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantType string
	}{
		{"没有当前账号", ErrNoCurrentAccount, http.StatusConflict, "conflict"},
		{"session 不可用", ErrAccountNoSession, http.StatusConflict, "conflict"},
		{"链接不合法", ErrCommentsBadRequest, http.StatusBadRequest, "bad_request"},
		{"超时", context.DeadlineExceeded, http.StatusGatewayTimeout, "comments_timeout"},
		{"联网失败", ErrCommentsFailed, http.StatusBadGateway, "comments_failed"},
		{"其他错误", errors.New("boom"), http.StatusInternalServerError, "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{
				GetComments: func(context.Context, CommentsQuery) (CommentsResponse, error) {
					return CommentsResponse{}, tt.err
				},
			}).Handler()

			rec := doRequest(t, h, http.MethodGet,
				"/api/v1/links/comments?link=https://t.me/a/1", nil)
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
