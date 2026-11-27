package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// cacheHandler 返回注入了给定缓存回调的处理器。
func cacheHandler(t *testing.T,
	stats func() (CacheStats, error),
	clear func() (CacheStats, error)) http.Handler {
	t.Helper()
	cfg := Config{Addr: "127.0.0.1:0"}
	if stats != nil {
		cfg.GetCacheStats = func(context.Context) (CacheStats, error) { return stats() }
	}
	if clear != nil {
		cfg.ClearCache = func(context.Context) (CacheStats, error) { return clear() }
	}
	return New(cfg).Handler()
}

// TestCacheStats 确认规模接口按约定返回三个计数。
func TestCacheStats(t *testing.T) {
	h := cacheHandler(t, func() (CacheStats, error) {
		return CacheStats{Messages: 12, Peers: 3, Threads: 1}, nil
	}, nil)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/cache", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}

	var resp CacheStats
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Messages != 12 || resp.Peers != 3 || resp.Threads != 1 {
		t.Errorf("统计 = %+v，期望 12/3/1", resp)
	}
}

// TestCacheClear 确认清空接口返回删除的行数。
func TestCacheClear(t *testing.T) {
	h := cacheHandler(t, nil, func() (CacheStats, error) {
		return CacheStats{Messages: 5}, nil
	})

	rec := doRequest(t, h, http.MethodDelete, "/api/v1/cache", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}

	var resp CacheStats
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Messages != 5 {
		t.Errorf("删除行数 = %+v，期望 5", resp)
	}
}

// TestCacheNotImplemented 确认未接入缓存管理时返回 501。
func TestCacheNotImplemented(t *testing.T) {
	h := cacheHandler(t, nil, nil)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		rec := doRequest(t, h, method, "/api/v1/cache", nil)
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s 状态码 = %d，期望 501", method, rec.Code)
		}
	}
}

// TestCacheStatsError 确认统计失败时返回 500 而不是空数据。
func TestCacheStatsError(t *testing.T) {
	h := cacheHandler(t, func() (CacheStats, error) {
		return CacheStats{}, errors.New("database is locked")
	}, nil)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/cache", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", rec.Code)
	}
}
