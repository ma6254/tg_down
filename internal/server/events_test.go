package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDownloadEventsStream 确认 SSE 帧格式与初始 ping。
func TestDownloadEventsStream(t *testing.T) {
	hub := NewEventHub()
	h := New(Config{EventHub: hub}).Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	// handler 阻塞在事件循环里，等它订阅上再发事件，避免早期事件丢失。
	waitSubscribers(t, hub, 1)
	hub.Publish(DownloadEvent{Type: "task.update", Data: DownloadTask{ID: 1, Status: DownloadStatusDone}})
	hub.Publish(DownloadEvent{Type: "task.progress", Data: map[string]any{"id": 1, "offset": 1024}})
	hub.Publish(DownloadEvent{Type: "task.deleted", Data: map[string]any{"id": 1}})
	hub.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("事件流没有在 hub 关闭后结束")
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("X-Accel-Buffering = %q", rec.Header().Get("X-Accel-Buffering"))
	}

	body := rec.Body.String()
	for _, want := range []string{
		"event: ping\ndata: {}",
		"event: task.update",
		"event: task.progress",
		"event: task.deleted",
		`"offset":1024`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("响应缺少 %q：\n%s", want, body)
		}
	}
}

// TestDownloadEventsNotImplemented 未接入事件源时返回 501。
func TestDownloadEventsNotImplemented(t *testing.T) {
	h := New(Config{}).Handler()
	rec := doJSONRequest(t, h, http.MethodGet, "/api/v1/events", "", nil)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("状态码 = %d，期望 501", rec.Code)
	}
}

// waitSubscribers 等 hub 上出现至少 want 个订阅者；只在测试主 goroutine 里调用。
func waitSubscribers(t *testing.T, hub *EventHub, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		hub.mu.Lock()
		n := len(hub.subs)
		hub.mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("订阅者数 = %d，期望 %d", n, want)
		}
		time.Sleep(time.Millisecond)
	}
}
