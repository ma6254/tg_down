package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestDownloadCreateValidation 覆盖创建接口的参数校验。
func TestDownloadCreateValidation(t *testing.T) {
	called := false
	h := New(Config{
		CreateDownloads: func(context.Context, DownloadCreateRequest) (DownloadCreateResponse, error) {
			called = true
			return DownloadCreateResponse{}, nil
		},
	}).Handler()

	tooMany := make([]string, maxDownloadLinks+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://t.me/a/%d", i+1)
	}
	tooManyBody, err := json.Marshal(DownloadCreateRequest{Links: tooMany})
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
		{"超过数量上限", string(tooManyBody)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads", tt.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d，期望 400，body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	if called {
		t.Error("校验失败时不应调用业务层")
	}
}

// TestDownloadCreateSuccess 覆盖创建成功时的响应结构。
func TestDownloadCreateSuccess(t *testing.T) {
	var got DownloadCreateRequest
	h := New(Config{
		CreateDownloads: func(_ context.Context, req DownloadCreateRequest) (DownloadCreateResponse, error) {
			got = req
			return DownloadCreateResponse{Created: []uint{1}}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads",
		`{"links":["  https://t.me/a/1  ","https://t.me/a/2"],"overwrite":true,"single":true,"allow_over_limit":true}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201，body=%s", rec.Code, rec.Body.String())
	}
	if len(got.Links) != 2 || got.Links[0] != "https://t.me/a/1" {
		t.Fatalf("业务层收到 links = %#v", got.Links)
	}
	if !got.Overwrite {
		t.Error("overwrite 没有传给业务层")
	}
	if !got.Single {
		t.Error("single 没有传给业务层")
	}
	if !got.AllowOverLimit {
		t.Error("allow_over_limit 没有传给业务层")
	}

	var resp DownloadCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Created) != 1 || resp.Created[0] != 1 {
		t.Errorf("created = %#v", resp.Created)
	}
	if resp.Existing == nil {
		t.Error("existing 应为空数组而不是 null")
	}
}

// TestDownloadListValidation 覆盖列表接口的筛选参数校验。
func TestDownloadListValidation(t *testing.T) {
	h := New(Config{
		ListDownloads: func(context.Context, DownloadListQuery) ([]DownloadTask, error) {
			t.Fatal("校验失败时不应调用业务层")
			return nil, nil
		},
	}).Handler()

	for _, path := range []string{
		"/api/v1/downloads?status=unknown",
		"/api/v1/downloads?limit=0",
		"/api/v1/downloads?limit=201",
		"/api/v1/downloads?limit=abc",
	} {
		rec := doJSONRequest(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
		}
	}
}

// TestDownloadListSuccess 覆盖列表正常返回与空结果不是 null。
func TestDownloadListSuccess(t *testing.T) {
	var got DownloadListQuery
	h := New(Config{
		ListDownloads: func(_ context.Context, query DownloadListQuery) ([]DownloadTask, error) {
			got = query
			return nil, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads?status=running&limit=20", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if got.Status != "running" || got.Limit != 20 {
		t.Errorf("业务层收到查询 = %+v", got)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("空列表应返回 []，实际 %s", rec.Body.String())
	}
}

// TestDownloadStats 覆盖合计速度接口：字面量路由优先于 {id}，空载也返回 200。
func TestDownloadStats(t *testing.T) {
	var called bool
	h := New(Config{
		GetDownloadStats: func(context.Context) DownloadStats {
			called = true
			return DownloadStats{TotalSpeed: 3500, RunningTasks: 2}
		},
		// 路由若把 stats 当成任务 ID，这里会失败。
		GetDownload: func(context.Context, uint) (DownloadTask, error) {
			t.Fatal("GET /downloads/stats 不应命中任务详情")
			return DownloadTask{}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/stats", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cc)
	}
	var stats DownloadStats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if stats.TotalSpeed != 3500 || stats.RunningTasks != 2 {
		t.Errorf("stats = %+v", stats)
	}
	if !called {
		t.Error("业务层没有被调用")
	}
}

// TestDownloadSpeedHistory 覆盖历史速度接口的参数解析与响应。
func TestDownloadSpeedHistory(t *testing.T) {
	var got DownloadSpeedHistoryQuery
	h := New(Config{
		GetDownloadSpeedHistory: func(_ context.Context, query DownloadSpeedHistoryQuery) (DownloadSpeedHistory, error) {
			got = query
			return DownloadSpeedHistory{
				From:           100,
				To:             200,
				Bucket:         1,
				SampleInterval: 1,
				StatsSince:     100,
				AverageSpeed:   300,
				PeakSpeed:      500,
				Points: []DownloadSpeedPoint{
					{At: 100, TotalSpeed: 300, RunningTasks: 2},
				},
			}, nil
		},
		// 路由若把 stats/history 当成任务详情，这里会失败。
		GetDownload: func(context.Context, uint) (DownloadTask, error) {
			t.Fatal("GET /downloads/stats/history 不应命中任务详情")
			return DownloadTask{}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet,
		"/api/v1/downloads/stats/history?from=100&to=200&bucket=1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if got.From.Unix() != 100 || got.To.Unix() != 200 || got.Bucket != time.Second {
		t.Errorf("业务层收到查询 = %+v", got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cc)
	}

	var resp DownloadSpeedHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if resp.AverageSpeed != 300 || resp.PeakSpeed != 500 || resp.SampleInterval != 1 {
		t.Errorf("响应汇总 = %+v", resp)
	}
	if resp.Bucket != 1 || resp.StatsSince != 100 {
		t.Errorf("bucket/stats_since = %d/%d，期望 1/100", resp.Bucket, resp.StatsSince)
	}
	if len(resp.Points) != 1 || resp.Points[0].TotalSpeed != 300 || resp.Points[0].RunningTasks != 2 {
		t.Errorf("points = %+v", resp.Points)
	}
}

// TestDownloadSpeedHistoryDefaults 确认省略参数时默认最近 30 分钟、自动粒度。
func TestDownloadSpeedHistoryDefaults(t *testing.T) {
	var got DownloadSpeedHistoryQuery
	h := New(Config{
		GetDownloadSpeedHistory: func(_ context.Context, query DownloadSpeedHistoryQuery) (DownloadSpeedHistory, error) {
			got = query
			return DownloadSpeedHistory{Points: []DownloadSpeedPoint{}}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/stats/history", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if got.Bucket != 0 {
		t.Errorf("默认 bucket = %v，期望 0（自动）", got.Bucket)
	}
	if span := got.To.Sub(got.From); span < 29*time.Minute || span > 31*time.Minute {
		t.Errorf("默认窗口 = %v，期望约 30 分钟", span)
	}
}

// TestDownloadSpeedStatsReset 覆盖清空平均/峰值接口。
func TestDownloadSpeedStatsReset(t *testing.T) {
	resetAt := time.Unix(1763800000, 0)
	h := New(Config{
		ResetDownloadSpeedStats: func(context.Context) time.Time {
			return resetAt
		},
		// 路由若命中任务操作或详情，这里会失败。
		ControlDownload: func(context.Context, uint, string) (DownloadTask, error) {
			t.Fatal("POST /downloads/stats/reset 不应命中任务操作")
			return DownloadTask{}, nil
		},
		GetDownload: func(context.Context, uint) (DownloadTask, error) {
			t.Fatal("POST /downloads/stats/reset 不应命中任务详情")
			return DownloadTask{}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost,
		"/api/v1/downloads/stats/reset", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	var resp DownloadSpeedStatsReset
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !resp.OK || resp.ResetAt != resetAt.Unix() {
		t.Errorf("响应 = %+v，期望 ok=true reset_at=%d", resp, resetAt.Unix())
	}
}

// TestDownloadSpeedHistoryValidation 覆盖历史速度接口的参数校验。
func TestDownloadSpeedHistoryValidation(t *testing.T) {
	h := New(Config{
		GetDownloadSpeedHistory: func(context.Context, DownloadSpeedHistoryQuery) (DownloadSpeedHistory, error) {
			t.Fatal("参数非法时不应调用业务层")
			return DownloadSpeedHistory{}, nil
		},
	}).Handler()

	paths := []string{
		"/api/v1/downloads/stats/history?from=abc",
		"/api/v1/downloads/stats/history?to=abc",
		"/api/v1/downloads/stats/history?from=0&to=100",
		"/api/v1/downloads/stats/history?from=200&to=100",
		"/api/v1/downloads/stats/history?from=100&to=100000",
		"/api/v1/downloads/stats/history?from=100&to=200&bucket=0",
		"/api/v1/downloads/stats/history?from=100&to=200&bucket=3601",
		"/api/v1/downloads/stats/history?from=100&to=86000&bucket=1",
	}
	for _, path := range paths {
		rec := doJSONRequest(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400，body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// TestDownloadGet 覆盖 ID 解析与 404 映射。
func TestDownloadGet(t *testing.T) {
	h := New(Config{
		GetDownload: func(_ context.Context, id uint) (DownloadTask, error) {
			if id == 42 {
				return DownloadTask{ID: 42, Status: DownloadStatusDone}, nil
			}
			return DownloadTask{}, ErrDownloadNotFound
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/abc", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 ID 状态码 = %d，期望 400", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/42", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodGet, "/api/v1/downloads/7", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在状态码 = %d，期望 404", rec.Code)
	}
}

// TestDownloadControl 覆盖暂停/恢复/重试与未知动作。
func TestDownloadControl(t *testing.T) {
	var gotAction string
	h := New(Config{
		ControlDownload: func(_ context.Context, id uint, action string) (DownloadTask, error) {
			gotAction = action
			if action == "retry" {
				return DownloadTask{}, ErrDownloadConflict
			}
			return DownloadTask{ID: id, Status: DownloadStatusPaused}, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/1/pause", "", nil)
	if rec.Code != http.StatusOK || gotAction != "pause" {
		t.Fatalf("pause 状态码 = %d，action = %q", rec.Code, gotAction)
	}

	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/1/retry", "", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("冲突状态码 = %d，期望 409", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/1/explode", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知动作状态码 = %d，期望 404", rec.Code)
	}
}

// TestDownloadDelete 覆盖 delete_file 参数与删除结果。
func TestDownloadDelete(t *testing.T) {
	var gotDeleteFile bool
	h := New(Config{
		DeleteDownload: func(_ context.Context, id uint, deleteFile bool) error {
			gotDeleteFile = deleteFile
			if id != 1 {
				return ErrDownloadNotFound
			}
			return nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodDelete, "/api/v1/downloads/1?delete_file=yes", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法 delete_file 状态码 = %d，期望 400", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodDelete, "/api/v1/downloads/1?delete_file=true", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if !gotDeleteFile {
		t.Error("delete_file 没有传给业务层")
	}

	rec = doJSONRequest(t, h, http.MethodDelete, "/api/v1/downloads/2", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在状态码 = %d，期望 404", rec.Code)
	}
}

// TestDownloadNotImplemented 未注入业务实现时返回 501。
func TestDownloadNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/downloads", `{"links":["https://t.me/a/1"]}`},
		{http.MethodGet, "/api/v1/downloads", ""},
		{http.MethodGet, "/api/v1/downloads/1", ""},
		{http.MethodGet, "/api/v1/downloads/stats", ""},
		{http.MethodGet, "/api/v1/downloads/stats/history", ""},
		{http.MethodPost, "/api/v1/downloads/stats/reset", ""},
		{http.MethodPost, "/api/v1/downloads/1/pause", ""},
		{http.MethodDelete, "/api/v1/downloads/1", ""},
	}
	for _, tt := range cases {
		rec := doJSONRequest(t, h, tt.method, tt.path, tt.body, nil)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s 状态码 = %d，期望 501", tt.method, tt.path, rec.Code)
		}
	}
}

// TestDownloadErrorMapping 确认业务错误到 HTTP 状态码的映射。
func TestDownloadErrorMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"不存在", ErrDownloadNotFound, http.StatusNotFound},
		{"状态冲突", ErrDownloadConflict, http.StatusConflict},
		{"没有当前账号", ErrNoCurrentAccount, http.StatusConflict},
		{"session 不可用", ErrAccountNoSession, http.StatusConflict},
		{"解析超时", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"查询失败", ErrPreviewFailed, http.StatusBadGateway},
		{"其他错误", errors.New("boom"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(Config{
				CreateDownloads: func(context.Context, DownloadCreateRequest) (DownloadCreateResponse, error) {
					return DownloadCreateResponse{}, tt.err
				},
			}).Handler()

			rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads",
				`{"links":["https://t.me/a/1"]}`, nil)
			if rec.Code != tt.wantCode {
				t.Fatalf("状态码 = %d，期望 %d，body=%s", rec.Code, tt.wantCode, rec.Body.String())
			}
			var body ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("解析错误响应: %v", err)
			}
			if body.Error.Code == "" {
				t.Error("错误码为空")
			}
		})
	}
}
