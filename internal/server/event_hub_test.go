package server

import (
	"context"
	"testing"
	"time"
)

// TestEventHubPublish 确认订阅者能收到广播，取消后通道关闭。
func TestEventHubPublish(t *testing.T) {
	hub := NewEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := hub.Subscribe(ctx)
	hub.Publish(DownloadEvent{Type: "task.update", Data: map[string]any{"id": 1}})

	select {
	case event := <-ch:
		if event.Type != "task.update" {
			t.Errorf("事件类型 = %q", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("没有收到事件")
	}

	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("取消后通道应关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("取消后通道未关闭")
	}
}

// TestEventHubClose 确认关闭 hub 会关闭所有订阅，且之后 Publish 不会 panic。
func TestEventHubClose(t *testing.T) {
	hub := NewEventHub()
	ch := hub.Subscribe(context.Background())

	hub.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("Close 后通道应关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("Close 后通道未关闭")
	}

	// 关闭后再 Publish / Subscribe 都不应 panic。
	hub.Publish(DownloadEvent{Type: "task.update"})
	late := hub.Subscribe(context.Background())
	select {
	case _, ok := <-late:
		if ok {
			t.Error("关闭后的新订阅应立即关闭")
		}
	case <-time.After(time.Second):
		t.Fatal("关闭后的新订阅未关闭")
	}
}

// TestEventHubSlowSubscriber 确认慢订阅者只丢事件，不阻塞广播。
func TestEventHubSlowSubscriber(t *testing.T) {
	hub := NewEventHub()
	ch := hub.Subscribe(context.Background())

	// 缓冲为 64，发 200 条不能阻塞。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			hub.Publish(DownloadEvent{Type: "task.progress"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("慢订阅者阻塞了广播")
	}

	// 至少能读到一些事件，且不会因为丢弃而卡住。
	if len(ch) == 0 {
		t.Fatal("订阅者一个事件都没收到")
	}
}
