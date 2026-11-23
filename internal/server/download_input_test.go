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

	"github.com/ma6254/tg_down/internal/downloadinput"
)

// TestDownloadDraftGet 覆盖默认草稿响应，并确认静态段优先于 {id}。
func TestDownloadDraftGet(t *testing.T) {
	h := New(Config{
		GetDownloadDraft: func(context.Context) (DownloadDraft, error) {
			return NewDownloadDraft(downloadinput.Draft{
				Links:     "https://t.me/a/1",
				Cache:     &downloadinput.Cache{V: 1, Summary: downloadinput.CacheSummary{Total: 1, OK: 1}},
				UpdatedAt: time.Unix(1763500000, 0),
			}), nil
		},
		GetDownload: func(context.Context, uint) (DownloadTask, error) {
			t.Fatal("GET /downloads/draft 不应落到 {id} 处理器")
			return DownloadTask{}, nil
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/draft", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	var got DownloadDraft
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Links != "https://t.me/a/1" || got.UpdatedAt != 1763500000 {
		t.Errorf("草稿响应异常: %+v", got)
	}
	if got.Cache == nil || got.Cache.Summary.Total != 1 {
		t.Errorf("缓存未返回: %+v", got.Cache)
	}
}

// TestDownloadDraftPut 覆盖保存请求体解析、缓存原样传递与错误映射。
func TestDownloadDraftPut(t *testing.T) {
	var got DownloadDraftInput
	failInvalid := false
	h := New(Config{
		SaveDownloadDraft: func(_ context.Context, input DownloadDraftInput) (DownloadDraft, error) {
			if failInvalid {
				return DownloadDraft{}, fmt.Errorf("%w: links 超过 256 KiB 上限", ErrDownloadDraftInvalid)
			}
			got = input
			return NewDownloadDraft(downloadinput.Draft{
				Links:     input.Links,
				UpdatedAt: time.Unix(1763500000, 0),
			}), nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPut, "/api/v1/downloads/draft",
		`{"links":"https://t.me/a/1","cache":{"v":1}}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，body=%s", rec.Code, rec.Body.String())
	}
	if got.Links != "https://t.me/a/1" {
		t.Errorf("业务层收到 %+v", got)
	}
	if string(got.Cache) != `{"v":1}` {
		t.Errorf("cache 原样传递失败: %q", string(got.Cache))
	}

	// 省略 cache 时业务层拿到 nil，用来区分「保留旧缓存」。
	rec = doJSONRequest(t, h, http.MethodPut, "/api/v1/downloads/draft", `{"links":""}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if got.Cache != nil {
		t.Errorf("省略 cache 时应为 nil，实际 %q", string(got.Cache))
	}

	// cache: null 必须原样传递，供业务层清空。
	rec = doJSONRequest(t, h, http.MethodPut, "/api/v1/downloads/draft",
		`{"links":"","cache":null}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if string(got.Cache) != "null" {
		t.Errorf("cache: null 应原样传递，实际 %q", string(got.Cache))
	}

	for _, body := range []string{`{"links":`, `{"links":1}`} {
		rec = doJSONRequest(t, h, http.MethodPut, "/api/v1/downloads/draft", body, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s 状态码 = %d，期望 400", body, rec.Code)
		}
	}

	failInvalid = true
	rec = doJSONRequest(t, h, http.MethodPut, "/api/v1/downloads/draft", `{"links":"x"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("业务校验失败状态码 = %d，期望 400", rec.Code)
	}
}

// TestDownloadInputNotImplemented 确认未接线时返回 501。
func TestDownloadInputNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/downloads/draft", ""},
		{http.MethodPut, "/api/v1/downloads/draft", "{}"},
		{http.MethodGet, "/api/v1/downloads/history", ""},
		{http.MethodPost, "/api/v1/downloads/history", "{}"},
		{http.MethodGet, "/api/v1/downloads/history/1", ""},
		{http.MethodGet, "/api/v1/downloads/history/1/cover", ""},
		{http.MethodDelete, "/api/v1/downloads/history/1", ""},
		{http.MethodDelete, "/api/v1/downloads/history", ""},
	}
	for _, tt := range tests {
		rec := doJSONRequest(t, h, tt.method, tt.path, tt.body, nil)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s 状态码 = %d，期望 501", tt.method, tt.path, rec.Code)
		}
	}
}

// TestDownloadHistoryList 覆盖 limit 校验与空列表序列化。
func TestDownloadHistoryList(t *testing.T) {
	var gotLimit int
	h := New(Config{
		ListDownloadHistory: func(_ context.Context, limit int) (DownloadHistoryList, error) {
			gotLimit = limit
			return NewDownloadHistoryList(nil), nil
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if gotLimit != downloadinput.HistoryLimit {
		t.Errorf("默认 limit = %d，期望 %d", gotLimit, downloadinput.HistoryLimit)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("空列表应序列化为 []，实际 %s", rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/downloads/history?limit=5", nil)
	if rec.Code != http.StatusOK || gotLimit != 5 {
		t.Errorf("limit=5 未透传: code=%d limit=%d", rec.Code, gotLimit)
	}

	for _, query := range []string{"limit=0", "limit=21", "limit=abc"} {
		rec = doRequest(t, h, http.MethodGet, "/api/v1/downloads/history?"+query, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400", query, rec.Code)
		}
	}
}

// newHistoryEntry 构造一条领域历史记录。
func newHistoryEntry() downloadinput.Entry {
	created := time.Unix(1763500000, 0)
	return downloadinput.Entry{
		ID:              3,
		Links:           "https://t.me/a/1",
		LineCount:       1,
		FirstLine:       "https://t.me/a/1",
		Title:           "[图片]",
		ExpandTextLinks: true,
		UseCount:        2,
		Cache: &downloadinput.Cache{
			V:       1,
			Summary: downloadinput.CacheSummary{Total: 3, OK: 3},
			Items:   []downloadinput.CacheItem{{MessageID: 2522}},
		},
		CacheTruncated: true,
		CreatedAt:      created,
		LastUsedAt:     created,
	}
}

// TestDownloadHistoryCreate 覆盖新建/重复记录的状态码。
func TestDownloadHistoryCreate(t *testing.T) {
	created := true
	h := New(Config{
		RecordDownloadHistory: func(_ context.Context, input DownloadHistoryInput) (DownloadHistoryDetail, bool, error) {
			if input.Links == "" {
				return DownloadHistoryDetail{}, false, fmt.Errorf("%w: links 为空", ErrDownloadHistoryInvalid)
			}
			if string(input.Cache) != `{"v":1}` {
				t.Errorf("cache 未透传: %q", string(input.Cache))
			}
			return NewDownloadHistoryDetail(newHistoryEntry()), created, nil
		},
	}).Handler()

	rec := doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/history",
		`{"links":"https://t.me/a/1","expand_text_links":true,"cache":{"v":1}}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("新建状态码 = %d，期望 201，body=%s", rec.Code, rec.Body.String())
	}
	var detail DownloadHistoryDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != 3 || detail.Links == "" || detail.Cache == nil {
		t.Errorf("详情响应异常: %+v", detail)
	}

	created = false
	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/history",
		`{"links":"https://t.me/a/1","cache":{"v":1}}`, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("重复记录状态码 = %d，期望 200", rec.Code)
	}

	rec = doJSONRequest(t, h, http.MethodPost, "/api/v1/downloads/history", `{"links":""}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法输入状态码 = %d，期望 400", rec.Code)
	}
}

// TestDownloadHistoryEndpoints 覆盖详情、删除与清空。
func TestDownloadHistoryEndpoints(t *testing.T) {
	notFound := errors.New("记录不存在")
	h := New(Config{
		GetDownloadHistory: func(_ context.Context, id uint) (DownloadHistoryDetail, error) {
			if id == 404 {
				return DownloadHistoryDetail{}, fmt.Errorf("%w: %v", ErrDownloadHistoryNotFound, notFound)
			}
			return NewDownloadHistoryDetail(newHistoryEntry()), nil
		},
		DeleteDownloadHistory: func(_ context.Context, id uint) error {
			if id == 404 {
				return fmt.Errorf("%w: %v", ErrDownloadHistoryNotFound, notFound)
			}
			return nil
		},
		ClearDownloadHistory: func(context.Context) (int, error) { return 12, nil },
	}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/history/3", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("详情状态码 = %d，期望 200", rec.Code)
	}
	rec = doRequest(t, h, http.MethodGet, "/api/v1/downloads/history/404", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在状态码 = %d，期望 404", rec.Code)
	}
	for _, path := range []string{"/api/v1/downloads/history/0", "/api/v1/downloads/history/abc"} {
		rec = doRequest(t, h, http.MethodGet, path, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
		}
	}

	rec = doRequest(t, h, http.MethodDelete, "/api/v1/downloads/history/3", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("删除响应异常: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, h, http.MethodDelete, "/api/v1/downloads/history/404", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("删除不存在状态码 = %d，期望 404", rec.Code)
	}

	rec = doRequest(t, h, http.MethodDelete, "/api/v1/downloads/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("清空状态码 = %d，期望 200", rec.Code)
	}
	var cleared struct {
		OK      bool `json:"ok"`
		Removed int  `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	if !cleared.OK || cleared.Removed != 12 {
		t.Errorf("清空响应异常: %+v", cleared)
	}
}

// TestDownloadHistoryCover 覆盖封面的字节返回与缺失映射。
func TestDownloadHistoryCover(t *testing.T) {
	cover := []byte("jpeg-bytes")
	h := New(Config{
		GetDownloadHistoryCover: func(_ context.Context, id uint) (MediaThumbnail, error) {
			switch id {
			case 1:
				return NewMediaThumbnail(cover), nil
			case 2:
				return MediaThumbnail{}, fmt.Errorf("%w: 这条历史没有封面", ErrThumbnailNotFound)
			case 3:
				return MediaThumbnail{}, fmt.Errorf("%w: %v", ErrDownloadHistoryNotFound, errors.New("记录不存在"))
			default:
				return MediaThumbnail{}, errors.New("数据库错误")
			}
		},
	}).Handler()

	rec := doRequest(t, h, http.MethodGet, "/api/v1/downloads/history/1/cover", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Body.String() != string(cover) {
		t.Errorf("封面字节 = %q", rec.Body.String())
	}

	for _, path := range []string{
		"/api/v1/downloads/history/2/cover",
		"/api/v1/downloads/history/3/cover",
	} {
		rec = doRequest(t, h, http.MethodGet, path, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", path, rec.Code)
		}
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/downloads/history/4/cover", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("内部错误状态码 = %d，期望 500", rec.Code)
	}
	for _, path := range []string{
		"/api/v1/downloads/history/abc/cover",
		"/api/v1/downloads/history/0/cover",
	} {
		rec = doRequest(t, h, http.MethodGet, path, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400", path, rec.Code)
		}
	}
}

// TestNewDownloadDraftOmitsEmptyUpdatedAt 确认没有 updated_at 时字段被省略。
func TestNewDownloadDraftOmitsEmptyUpdatedAt(t *testing.T) {
	data, err := json.Marshal(NewDownloadDraft(downloadinput.Draft{Links: ""}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "updated_at") {
		t.Errorf("零值 updated_at 应省略: %s", data)
	}
}

// TestNewDownloadHistoryEntry 覆盖摘要/会话的映射与空缓存。
func TestNewDownloadHistoryEntry(t *testing.T) {
	entry := NewDownloadHistoryEntry(newHistoryEntry())
	if entry.Summary == nil || entry.Summary.Total != 3 {
		t.Errorf("summary 未映射: %+v", entry.Summary)
	}
	if entry.Title != "[图片]" {
		t.Errorf("title = %q，期望 [图片]", entry.Title)
	}
	if entry.CreatedAt != 1763500000 || entry.LastUsedAt != 1763500000 {
		t.Errorf("时间映射异常: %+v", entry)
	}

	empty := NewDownloadHistoryEntry(downloadinput.Entry{ID: 1})
	if empty.Summary != nil || empty.Chats != nil {
		t.Errorf("没有缓存时不应返回 summary/chats: %+v", empty)
	}

	list := NewDownloadHistoryList(nil)
	if list.Items == nil {
		t.Error("空列表应为 [] 而不是 nil")
	}
}
