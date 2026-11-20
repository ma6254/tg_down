package server

import (
	"context"
	"sync"
)

// EventHub 是进程内的 SSE 广播器，GET /api/v1/events 的事件源。
//
// 订阅者各自有一个有界缓冲；写不进去（前端卡住）时直接丢事件，
// 不让慢客户端阻塞下载 worker。断线重连后前端会全量拉一次任务列表，
// 丢掉的中间进度不影响最终状态。
type EventHub struct {
	mu     sync.Mutex
	subs   map[chan DownloadEvent]struct{}
	closed bool
}

// NewEventHub 创建一个还没有订阅者的广播器。
func NewEventHub() *EventHub {
	return &EventHub{subs: make(map[chan DownloadEvent]struct{})}
}

// Subscribe 注册一个订阅者；ctx 结束时自动注销并关闭通道。
//
// 广播器已关闭时返回一个立即关闭的通道，调用方无需区分。
func (h *EventHub) Subscribe(ctx context.Context) <-chan DownloadEvent {
	ch := make(chan DownloadEvent, 64)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(ch)
		return ch
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	go func() {
		<-ctx.Done()
		h.remove(ch)
	}()
	return ch
}

// Publish 向所有订阅者非阻塞广播；慢订阅者丢弃本条事件。
func (h *EventHub) Publish(event DownloadEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for ch := range h.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

// remove 注销一个订阅者；重复调用安全。
func (h *EventHub) remove(ch chan DownloadEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; !ok {
		return
	}
	delete(h.subs, ch)
	close(ch)
}

// Close 关闭全部订阅（服务停止时调用）；之后 Publish 是空操作。
func (h *EventHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}
