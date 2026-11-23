package downloadinput

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ma6254/tg_down/internal/database"
)

// openTestStore 用内存数据库构造仓库。
func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Install(); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

// TestValidateLinks 覆盖空文本、行数上限与字节上限。
func TestValidateLinks(t *testing.T) {
	if err := ValidateLinks(""); err != nil {
		t.Errorf("空文本应合法: %v", err)
	}
	if err := ValidateLinks(" https://t.me/a/1 \r\n\r\nhttps://t.me/a/2 "); err != nil {
		t.Errorf("普通文本应合法: %v", err)
	}

	if err := ValidateLinks(strings.Repeat("a", MaxLinksBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("超过字节上限应返回 ErrInvalid，实际 %v", err)
	}

	lines := strings.Repeat("x\n", MaxLines)
	if err := ValidateLinks(lines); err != nil {
		t.Errorf("%d 行应合法: %v", MaxLines, err)
	}
	if err := ValidateLinks(lines + "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("超过行数上限应返回 ErrInvalid，实际 %v", err)
	}
}

// TestInspectLinksNormalize 确认换行归一与空行丢弃。
func TestInspectLinksNormalize(t *testing.T) {
	normalized, lineCount, firstLine, err := inspectLinks("  https://t.me/a/1  \r\n\r\n\thttps://t.me/a/2\t\n")
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "https://t.me/a/1\nhttps://t.me/a/2" {
		t.Errorf("normalized = %q", normalized)
	}
	if lineCount != 2 {
		t.Errorf("lineCount = %d，期望 2", lineCount)
	}
	if firstLine != "https://t.me/a/1" {
		t.Errorf("firstLine = %q", firstLine)
	}
}

// TestHashIgnoresWhitespace 确认哈希按归一后的内容与开关计算。
func TestHashIgnoresWhitespace(t *testing.T) {
	a, _, _, _ := inspectLinks("https://t.me/a/1\nhttps://t.me/a/2")
	b, _, _, _ := inspectLinks("  https://t.me/a/1 \r\n\r\n https://t.me/a/2  ")
	if a == "" || a != b {
		t.Fatalf("归一结果应一致: %q / %q", a, b)
	}
	if Hash(a) != Hash(b) {
		t.Error("相同内容应得到相同哈希")
	}
	if Hash(a) == Hash(a+"\nextra") {
		t.Error("不同内容不应得到相同哈希")
	}
}

// TestParseCacheTrim 覆盖缓存白名单裁剪。
func TestParseCacheTrim(t *testing.T) {
	items := make([]CacheItem, 0, 30)
	for i := 0; i < 30; i++ {
		items = append(items, CacheItem{
			MessageID: i + 1,
			ChatTitle: strings.Repeat("频", 10),
			Text:      strings.Repeat("文", 300),
			MediaKind: "unknown",
			FileName:  strings.Repeat("f", 30),
			Size:      -1,
		})
	}
	chats := make([]CacheChat, 0, 8)
	for i := 0; i < 8; i++ {
		chats = append(chats, CacheChat{Kind: "bogus", Title: strings.Repeat("会", 300), Username: strings.Repeat("u", 100)})
	}
	failures := make([]CacheFailure, 0, 8)
	for i := 0; i < 8; i++ {
		failures = append(failures, CacheFailure{Link: "link", Error: strings.Repeat("错", 300)})
	}
	raw, err := json.Marshal(Cache{
		Summary:  CacheSummary{Total: -1, OK: -2, Failed: -3, NoMedia: -4, Expanded: -5},
		Chats:    chats,
		Items:    items,
		Failures: failures,
	})
	if err != nil {
		t.Fatal(err)
	}

	cache, err := ParseCache(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cache.V != CacheVersion {
		t.Errorf("V = %d，期望 %d", cache.V, CacheVersion)
	}
	if cache.Summary != (CacheSummary{}) {
		t.Errorf("负数计数未收敛: %+v", cache.Summary)
	}
	if len(cache.Items) != maxCacheItems {
		t.Errorf("items = %d，期望 %d", len(cache.Items), maxCacheItems)
	}
	if len(cache.Chats) != maxCacheChats {
		t.Errorf("chats = %d，期望 %d", len(cache.Chats), maxCacheChats)
	}
	if len(cache.Failures) != maxCacheFailures {
		t.Errorf("failures = %d，期望 %d", len(cache.Failures), maxCacheFailures)
	}
	if got := len([]rune(cache.Items[0].Text)); got != maxCacheText {
		t.Errorf("text 长度 = %d，期望 %d", got, maxCacheText)
	}
	if got := len([]rune(cache.Failures[0].Error)); got != maxCacheText {
		t.Errorf("error 长度 = %d，期望 %d", got, maxCacheText)
	}
	if cache.Items[0].MediaKind != "" {
		t.Errorf("未知媒体类型应被丢弃，实际 %q", cache.Items[0].MediaKind)
	}
	if cache.Items[0].Size != 0 {
		t.Errorf("负数大小应收敛为 0，实际 %d", cache.Items[0].Size)
	}
	if cache.Chats[0].Kind != "" {
		t.Errorf("未知会话类型应被丢弃，实际 %q", cache.Chats[0].Kind)
	}
}

// TestParseCacheThumbKey 覆盖封面缓存键的白名单校验与取用。
func TestParseCacheThumbKey(t *testing.T) {
	valid := strings.Repeat("a", 64)
	raw, err := json.Marshal(Cache{
		Summary: CacheSummary{Total: 3},
		Items: []CacheItem{
			{MessageID: 1, ThumbKey: valid},
			{MessageID: 2, ThumbKey: "../../../evil"},
			{MessageID: 3, ThumbKey: strings.Repeat("A", 64)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cache, err := ParseCache(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cache.Items[0].ThumbKey != valid {
		t.Errorf("合法键被丢弃: %q", cache.Items[0].ThumbKey)
	}
	if cache.Items[1].ThumbKey != "" || cache.Items[2].ThumbKey != "" {
		t.Errorf("非法键应被丢弃: %q / %q", cache.Items[1].ThumbKey, cache.Items[2].ThumbKey)
	}

	entry := Entry{Cache: cache}
	if got := entry.CoverThumbKey(); got != valid {
		t.Errorf("CoverThumbKey = %q，期望 %q", got, valid)
	}
	if (Entry{}).CoverThumbKey() != "" {
		t.Error("没有摘要时应返回空串")
	}
}

// TestLegacyDraftSwitches 确认能读出旧草稿里的两个开关。
func TestLegacyDraftSwitches(t *testing.T) {
	store := openTestStore(t)

	if switches, err := store.LegacyDraftSwitches(); err != nil || switches.Overwrite != nil || switches.ExpandTextLinks != nil {
		t.Errorf("空库应返回空开关；实际 %+v, %v", switches, err)
	}

	if _, err := store.db.SaveUIDraft(DraftKey,
		`{"links":"x","expand_text_links":true,"overwrite":true,"cache":null}`); err != nil {
		t.Fatal(err)
	}
	switches, err := store.LegacyDraftSwitches()
	if err != nil {
		t.Fatal(err)
	}
	if switches.ExpandTextLinks == nil || !*switches.ExpandTextLinks {
		t.Errorf("expand_text_links = %v，期望 true", switches.ExpandTextLinks)
	}
	if switches.Overwrite == nil || !*switches.Overwrite {
		t.Errorf("overwrite = %v，期望 true", switches.Overwrite)
	}

	if _, err := store.db.SaveUIDraft(DraftKey, `{"links":"x"}`); err != nil {
		t.Fatal(err)
	}
	if switches, err := store.LegacyDraftSwitches(); err != nil || switches.Overwrite != nil || switches.ExpandTextLinks != nil {
		t.Errorf("没有开关字段时应返回空；实际 %+v, %v", switches, err)
	}
}

// TestNormalizeHashes 确认旧版本按开关拆出的多条历史会合并成一条。
func TestNormalizeHashes(t *testing.T) {
	store := openTestStore(t)
	links := "https://t.me/a/1\nhttps://t.me/a/2"
	now := time.Now()

	older := &database.DownloadInput{
		Hash:            "old-hash-without-switches",
		Links:           links,
		LineCount:       2,
		FirstLine:       "https://t.me/a/1",
		ExpandTextLinks: false,
		Overwrite:       false,
		UseCount:        2,
		LastUsedAt:      now.Add(-time.Hour),
	}
	if _, err := store.db.SaveDownloadInput(older); err != nil {
		t.Fatal(err)
	}
	newer := &database.DownloadInput{
		Hash:            "old-hash-with-switches",
		Links:           links,
		LineCount:       2,
		FirstLine:       "https://t.me/a/1",
		ExpandTextLinks: true,
		Overwrite:       true,
		UseCount:        3,
		LastUsedAt:      now,
	}
	if _, err := store.db.SaveDownloadInput(newer); err != nil {
		t.Fatal(err)
	}

	if err := store.NormalizeHashes(); err != nil {
		t.Fatalf("规范化失败: %v", err)
	}

	rows, err := store.db.ListDownloadInputs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("规范化后应剩 1 条，实际 %d: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Hash != Hash(links) {
		t.Errorf("hash = %q，期望 %q", got.Hash, Hash(links))
	}
	if got.UseCount != 5 {
		t.Errorf("use_count = %d，期望 5", got.UseCount)
	}
	if !got.Overwrite || !got.ExpandTextLinks {
		t.Errorf("应保留最近一次记录的开关快照: %+v", got)
	}

	// 幂等：再次执行不报错也不改变结果。
	if err := store.NormalizeHashes(); err != nil {
		t.Fatalf("二次规范化失败: %v", err)
	}
	rows, err = store.db.ListDownloadInputs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UseCount != 5 {
		t.Errorf("二次规范化改变了数据: %+v", rows)
	}
}

// TestSummaryTitle 覆盖历史卡片标题的取值优先级。
func TestSummaryTitle(t *testing.T) {
	tests := []struct {
		name  string
		cache *Cache
		want  string
	}{
		{"没有缓存", nil, ""},
		{"正文优先", &Cache{Items: []CacheItem{{Text: "  第一行\n第二行  ", MediaKind: "photo"}}}, "第一行 第二行"},
		{"图片占位", &Cache{Items: []CacheItem{{MediaKind: "photo"}}}, "[图片]"},
		{"视频占位", &Cache{Items: []CacheItem{{MediaKind: "video"}}}, "[视频]"},
		{"音频占位", &Cache{Items: []CacheItem{{MediaKind: "audio"}}}, "[音频]"},
		{"文件占位", &Cache{Items: []CacheItem{{MediaKind: "document"}}}, "[文件]"},
		{"第一条无内容时用后续", &Cache{Items: []CacheItem{{}, {MediaKind: "video"}}}, "[视频]"},
		{"未知媒体不生成占位", &Cache{Items: []CacheItem{{MediaKind: "sticker"}}}, ""},
		{"长文本截断", &Cache{Items: []CacheItem{{Text: strings.Repeat("字", 200)}}}, strings.Repeat("字", maxSummaryTitle)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SummaryTitle(tt.cache); got != tt.want {
				t.Errorf("SummaryTitle = %q，期望 %q", got, tt.want)
			}
		})
	}
}

// TestParseCacheInvalid 覆盖非法 JSON、null 与缺省。
func TestParseCacheInvalid(t *testing.T) {
	if cache, err := ParseCache(nil); err != nil || cache != nil {
		t.Errorf("空输入应返回 nil, nil；实际 %v, %v", cache, err)
	}
	if cache, err := ParseCache([]byte("null")); err != nil || cache != nil {
		t.Errorf("null 应返回 nil, nil；实际 %v, %v", cache, err)
	}
	if _, err := ParseCache([]byte(`{"summary":{"total":"x"}}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("类型错误应返回 ErrInvalid，实际 %v", err)
	}
}

// TestStoreDraft 覆盖草稿的默认值、保存、缓存保留与清除。
func TestStoreDraft(t *testing.T) {
	store := openTestStore(t)

	draft, err := store.GetDraft()
	if err != nil {
		t.Fatalf("空库读取草稿失败: %v", err)
	}
	if draft.Links != "" || draft.Cache != nil || !draft.UpdatedAt.IsZero() {
		t.Fatalf("空库应返回默认草稿: %+v", draft)
	}

	saved, err := store.SaveDraft(SaveDraftRequest{
		Links: "https://t.me/a/1\nhttps://t.me/a/2",
	})
	if err != nil {
		t.Fatalf("保存草稿失败: %v", err)
	}
	if saved.Links == "" || saved.UpdatedAt.IsZero() {
		t.Fatalf("保存结果异常: %+v", saved)
	}

	cacheJSON := json.RawMessage(`{"v":1,"summary":{"total":2,"ok":2},"chats":[{"kind":"channel","title":"某频道"}]}`)
	saved, err = store.SaveDraft(SaveDraftRequest{
		Links: "https://t.me/a/1",
		Cache: cacheJSON,
	})
	if err != nil {
		t.Fatalf("带缓存保存失败: %v", err)
	}
	if saved.Cache == nil || saved.Cache.V != CacheVersion || saved.Cache.Summary.Total != 2 {
		t.Fatalf("缓存未落库: %+v", saved.Cache)
	}

	// 省略 cache 时保留已有摘要（自动保存只带输入的场景）。
	saved, err = store.SaveDraft(SaveDraftRequest{Links: "https://t.me/a/2"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Cache == nil {
		t.Fatal("省略 cache 时应保留已有摘要")
	}

	// null 显式清空。
	saved, err = store.SaveDraft(SaveDraftRequest{Links: "https://t.me/a/2", Cache: json.RawMessage("null")})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Cache != nil {
		t.Fatalf("cache: null 应清空摘要，实际 %+v", saved.Cache)
	}

	// 非法缓存丢弃，但输入仍保存。
	saved, err = store.SaveDraft(SaveDraftRequest{
		Links: "https://t.me/a/3",
		Cache: json.RawMessage(`{"summary":{"total":"bad"}}`),
	})
	if err != nil {
		t.Fatalf("非法缓存不应影响保存: %v", err)
	}
	if saved.Cache != nil || saved.Links != "https://t.me/a/3" {
		t.Fatalf("非法缓存处理异常: %+v", saved)
	}

	if _, err := store.SaveDraft(SaveDraftRequest{Links: strings.Repeat("a", MaxLinksBytes+1)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("超长输入应返回 ErrInvalid，实际 %v", err)
	}
}

// TestStoreRecord 覆盖去重、计数、缓存与保留条数。
func TestStoreRecord(t *testing.T) {
	store := openTestStore(t)

	cache := json.RawMessage(`{"v":1,"summary":{"total":25,"ok":20},"items":[{"message_id":1,"text":" 首条\n消息 ","media_kind":"video"}]}`)
	entry, created, err := store.Record(RecordRequest{
		Links: "https://t.me/a/1\r\n\r\nhttps://t.me/a/2",
		Cache: cache,
	})
	if err != nil {
		t.Fatalf("记录失败: %v", err)
	}
	if !created {
		t.Error("首次记录应新建")
	}
	if entry.ID == 0 || entry.UseCount != 1 {
		t.Errorf("记录结果异常: %+v", entry)
	}
	if entry.Title != "首条 消息" {
		t.Errorf("标题 = %q，期望 %q", entry.Title, "首条 消息")
	}
	if entry.LineCount != 2 || entry.FirstLine != "https://t.me/a/1" {
		t.Errorf("行数与首行异常: %+v", entry)
	}
	if entry.Cache == nil || !entry.CacheTruncated {
		t.Errorf("摘要缓存或截断标记异常: %+v", entry)
	}

	// 相同内容（不同空白）再次记录：不新增，次数 +1。
	again, created, err := store.Record(RecordRequest{
		Links: "  https://t.me/a/1  \n https://t.me/a/2 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("相同内容不应新建")
	}
	if again.ID != entry.ID || again.UseCount != 2 {
		t.Errorf("重复记录结果异常: %+v", again)
	}
	if again.Cache != nil {
		t.Errorf("未带缓存时应覆盖旧摘要，实际 %+v", again.Cache)
	}

	// 覆盖开关不同不新建：复用同一行并更新开关值。
	withOverwrite, created, err := store.Record(RecordRequest{
		Links:     "https://t.me/a/1\nhttps://t.me/a/2",
		Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || withOverwrite.ID != entry.ID {
		t.Errorf("覆盖开关不同不应新建: created=%v %+v", created, withOverwrite)
	}
	if !withOverwrite.Overwrite || withOverwrite.UseCount != 3 {
		t.Errorf("覆盖开关或次数未更新: %+v", withOverwrite)
	}

	// 展开开关不同也不新建：两个开关都只是全局配置的快照。
	withExpand, created, err := store.Record(RecordRequest{
		Links:           "https://t.me/a/1\nhttps://t.me/a/2",
		ExpandTextLinks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || withExpand.ID != entry.ID {
		t.Errorf("展开开关不同不应新建: created=%v %+v", created, withExpand)
	}
	if !withExpand.ExpandTextLinks || withExpand.UseCount != 4 {
		t.Errorf("展开开关或次数未更新: %+v", withExpand)
	}

	// 超过 20 条时只保留最近 20 条。
	for i := 0; i < 25; i++ {
		if _, _, err := store.Record(RecordRequest{Links: "https://t.me/bulk/" + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.db.ListDownloadInputs(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != HistoryLimit {
		t.Fatalf("保留条数 = %d，期望 %d", len(rows), HistoryLimit)
	}

	list, err := store.List(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != HistoryLimit {
		t.Errorf("List 条数 = %d，期望 %d", len(list), HistoryLimit)
	}

	if err := store.Delete(list[0].ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}

	removed, err := store.Clear()
	if err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	if removed != HistoryLimit-1 {
		t.Errorf("清空条数 = %d，期望 %d", removed, HistoryLimit-1)
	}
}
