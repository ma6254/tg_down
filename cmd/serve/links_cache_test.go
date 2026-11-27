package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/msgcache"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// fakeRPC 是 linkSession 的假实现，记录定位与读消息的次数。
type fakeRPC struct {
	resolved     mtproto.ResolveResult
	fetched      []mtproto.LinkResult
	readErr      error
	readCalls    int
	resolveCalls int
}

func (f *fakeRPC) ResolveTargets(context.Context, []link.Target) (mtproto.ResolveResult, error) {
	f.resolveCalls++
	return f.resolved, nil
}

// ResolveLinks 只在关闭缓存时被调用；缓存测试里只要被调用就算失败。
func (f *fakeRPC) ResolveLinks(context.Context, []link.Target) ([]mtproto.LinkResult, error) {
	f.readCalls++
	return nil, errors.New("关闭缓存时才会走 ResolveLinks")
}

func (f *fakeRPC) ReadMessages(context.Context, []mtproto.ResolvedTarget, int64) ([]mtproto.LinkResult, error) {
	f.readCalls++
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.fetched, nil
}

// fakeCache 是 linkCache 的内存实现。
type fakeCache struct {
	entries map[int]msgcache.Entry
	// peers 以 PeerKey 为键，模拟会话定位缓存。
	peers   map[string]mtproto.Peer
	saved   []mtproto.LinkResult
	gone    []int
	touches int
}

func (f *fakeCache) LoadPeers(_ int64, targets []link.Target) (map[int]mtproto.Peer, int64, error) {
	out := make(map[int]mtproto.Peer, len(targets))
	for i, target := range targets {
		key, ok := msgcache.PeerKey(target)
		if !ok {
			continue
		}
		if peer, ok := f.peers[key]; ok {
			out[i] = peer
		}
	}
	if len(out) == 0 {
		return out, 0, nil
	}
	return out, mtproto.MaxFileSize, nil
}

func (f *fakeCache) SavePeers(_ int64, _ int64, entries []msgcache.PeerEntry) error {
	if f.peers == nil {
		f.peers = make(map[string]mtproto.Peer, len(entries))
	}
	for _, entry := range entries {
		f.peers[entry.Key] = entry.Peer
	}
	return nil
}

func (f *fakeCache) Lookup(_, _ int64, ids []int) (map[int]msgcache.Entry, error) {
	out := make(map[int]msgcache.Entry, len(ids))
	for _, id := range ids {
		if entry, ok := f.entries[id]; ok {
			out[id] = entry
		}
	}
	return out, nil
}

func (f *fakeCache) Save(_, _ int64, results []mtproto.LinkResult) error {
	f.saved = append(f.saved, results...)
	// 模拟真实缓存：写回后下一轮就能命中。
	if f.entries == nil {
		f.entries = make(map[int]msgcache.Entry, len(results))
	}
	for _, result := range results {
		f.entries[result.MessageID] = msgcache.Entry{
			Result: result, Status: database.MessageStatusOK, Fresh: true,
		}
	}
	return nil
}

func (f *fakeCache) MarkGone(_, _ int64, ids []int) error {
	f.gone = append(f.gone, ids...)
	return nil
}

func (f *fakeCache) Touch(int64, []int) error {
	f.touches++
	return nil
}

var (
	testTarget = link.Target{Raw: "https://t.me/chan/7", Username: "chan", MessageID: 7}
	testPeer   = mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 42, Title: "频道"}
)

// newTestDeps 构造只依赖 fake 的预览依赖。
func newTestDeps(rpc *fakeRPC, cache linkCache) previewDeps {
	return previewDeps{rpc: rpc, cache: cache, accountID: 1}
}

// resolvedOne 构造一条已定位的链接。
func resolvedOne() mtproto.ResolveResult {
	return mtproto.ResolveResult{
		Targets: []mtproto.ResolvedTarget{{Target: testTarget, Peer: testPeer, MessageID: 7}},
		MaxSize: mtproto.MaxFileSize,
	}
}

// cachedResult 构造一条缓存里的结果。
func cachedResult(text string) mtproto.LinkResult {
	return mtproto.LinkResult{Target: testTarget, Chat: testPeer, MessageID: 7, Text: text}
}

// TestPreviewCacheHitSkipsRead 确认命中缓存后不再读消息，并把缓存时间透出给接口。
func TestPreviewCacheHitSkipsRead(t *testing.T) {
	fetchedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	rpc := &fakeRPC{resolved: resolvedOne()}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {
			Result:    cachedResult("缓存正文"),
			Status:    database.MessageStatusOK,
			Fresh:     true,
			FetchedAt: fetchedAt,
		},
	}}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if rpc.readCalls != 0 {
		t.Fatalf("命中缓存后读了 %d 次消息，期望 0", rpc.readCalls)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("结果数 = %d，期望 1", len(resp.Items))
	}
	if got := resp.Items[0]; got.Text != "缓存正文" || !got.Cached {
		t.Errorf("item = %+v，期望命中缓存的正文", got)
	}
	if got := resp.Items[0].CachedAt; got != fetchedAt.Unix() {
		t.Errorf("cached_at = %d，期望 %d", got, fetchedAt.Unix())
	}
}

// TestPreviewFullyCachedSkipsTelegram 确认会话和消息都命中时完全不联网。
func TestPreviewFullyCachedSkipsTelegram(t *testing.T) {
	cache := &fakeCache{
		peers: map[string]mtproto.Peer{"u:chan": testPeer},
		entries: map[int]msgcache.Entry{
			7: {Result: cachedResult("缓存正文"), Status: database.MessageStatusOK, Fresh: true},
		},
	}
	rpc := &fakeRPC{resolved: resolvedOne()}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if rpc.resolveCalls != 0 {
		t.Errorf("会话已缓存，仍定位了 %d 次", rpc.resolveCalls)
	}
	if rpc.readCalls != 0 {
		t.Errorf("消息已缓存，仍读了 %d 次", rpc.readCalls)
	}
	if got := resp.Items[0]; got.Text != "缓存正文" || !got.Cached {
		t.Errorf("item = %+v，期望命中缓存", got)
	}
}

// TestPreviewStoresResolvedPeers 确认联网定位后会把会话写回缓存，下次不再定位。
func TestPreviewStoresResolvedPeers(t *testing.T) {
	rpc := &fakeRPC{resolved: resolvedOne(), fetched: []mtproto.LinkResult{cachedResult("新正文")}}
	cache := &fakeCache{entries: map[int]msgcache.Entry{}}

	if _, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget}); err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if got := cache.peers["u:chan"]; got.ID != testPeer.ID {
		t.Errorf("会话缓存 = %+v，期望写入 %+v", got, testPeer)
	}

	// 第二轮：会话与消息都应在本地命中。
	rpc.resolveCalls, rpc.readCalls = 0, 0
	if _, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget}); err != nil {
		t.Fatalf("第二次预览失败: %v", err)
	}
	if rpc.resolveCalls != 0 || rpc.readCalls != 0 {
		t.Errorf("第二轮仍联网：resolve=%d read=%d", rpc.resolveCalls, rpc.readCalls)
	}
}

// TestPreviewMissReadsAndSaves 确认未命中时读消息并回写缓存。
func TestPreviewMissReadsAndSaves(t *testing.T) {
	rpc := &fakeRPC{
		resolved: resolvedOne(),
		fetched:  []mtproto.LinkResult{cachedResult("新正文")},
	}
	cache := &fakeCache{entries: map[int]msgcache.Entry{}}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if rpc.readCalls != 1 {
		t.Fatalf("读了 %d 次消息，期望 1", rpc.readCalls)
	}
	if len(cache.saved) != 1 || cache.saved[0].Text != "新正文" {
		t.Errorf("回写缓存 = %+v，期望一条新正文", cache.saved)
	}
	if got := resp.Items[0]; got.Cached {
		t.Error("新读到的结果不应标记为 Cached")
	}
}

// TestPreviewRefreshBypassesCache 确认 refresh 时不读缓存，直接重新拉取并回写。
func TestPreviewRefreshBypassesCache(t *testing.T) {
	rpc := &fakeRPC{resolved: resolvedOne(), fetched: []mtproto.LinkResult{cachedResult("新正文")}}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {Result: cachedResult("缓存正文"), Status: database.MessageStatusOK, Fresh: true},
	}}
	deps := newTestDeps(rpc, cache)
	deps.forceRefresh = true

	resp, err := deps.preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	if rpc.readCalls != 1 {
		t.Fatalf("forceRefresh 应重新读消息，实际读 %d 次", rpc.readCalls)
	}
	if got := resp.Items[0]; got.Text != "新正文" || got.Cached {
		t.Errorf("item = %+v，期望新读到的正文", got)
	}
}

// TestPreviewRefreshFailureSurfacesError 确认强制刷新时不降级旧快照。
func TestPreviewRefreshFailureSurfacesError(t *testing.T) {
	rpc := &fakeRPC{resolved: resolvedOne(), readErr: errors.New("连接失败")}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {Result: cachedResult("旧正文"), Status: database.MessageStatusOK, Fresh: true},
	}}
	deps := newTestDeps(rpc, cache)
	deps.forceRefresh = true

	resp, err := deps.preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	got := resp.Items[0]
	if got.Stale || got.Text != "" || got.Error == "" {
		t.Errorf("item = %+v，期望如实报错而不是降级旧快照", got)
	}
}

// TestPreviewStaleFallback 确认过期快照在重取失败时降级返回。
func TestPreviewStaleFallback(t *testing.T) {
	rpc := &fakeRPC{resolved: resolvedOne(), readErr: errors.New("连接失败")}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {Result: cachedResult("旧正文"), Status: database.MessageStatusOK, Fresh: false},
	}}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("重取失败不应让预览整体失败: %v", err)
	}
	if got := resp.Items[0]; !got.Stale || got.Text != "旧正文" || got.Error != "" {
		t.Errorf("item = %+v，期望降级返回旧快照并标记 Stale", got)
	}
}

// TestPreviewGoneKeepsSnapshot 确认上次判定失效的快照仍返回正文并标记 Gone。
func TestPreviewGoneKeepsSnapshot(t *testing.T) {
	rpc := &fakeRPC{resolved: resolvedOne()}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {Result: cachedResult("已删的正文"), Status: database.MessageStatusGone, Fresh: true},
	}}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	got := resp.Items[0]
	if !got.Gone || got.Text != "已删的正文" {
		t.Errorf("item = %+v，期望保留快照并标记 Gone", got)
	}
	if rpc.readCalls != 0 {
		t.Errorf("失效标记仍在有效期内，不应再读消息")
	}
}

// TestPreviewRecomputesMaxSize 确认命中缓存后用当前账号上限重算可下载标记。
//
// 缓存可能由 Premium 账号写入（4GB），普通账号读到同一行时必须按 2GB 判断。
func TestPreviewRecomputesMaxSize(t *testing.T) {
	result := cachedResult("大文件")
	result.Media = &mtproto.Media{MessageID: 7, Kind: mtproto.MediaKindDocument, Size: 3 << 30}

	rpc := &fakeRPC{resolved: resolvedOne()}
	cache := &fakeCache{entries: map[int]msgcache.Entry{
		7: {Result: result, Status: database.MessageStatusOK, Fresh: true},
	}}

	resp, err := newTestDeps(rpc, cache).preview(context.Background(), server.PreviewRequest{},
		nil, []link.Target{testTarget})
	if err != nil {
		t.Fatalf("预览失败: %v", err)
	}
	media := resp.Items[0].Media
	if media == nil {
		t.Fatal("媒体丢失")
	}
	if media.MaxSize != mtproto.MaxFileSize {
		t.Errorf("max_size = %d，期望 %d", media.MaxSize, mtproto.MaxFileSize)
	}
	if media.Downloadable {
		t.Error("3GB 文件在 2GB 上限下不应标记为可下载")
	}
}
