package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/download/manager"
	"github.com/ma6254/tg_down/internal/downloadinput"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/thumbcache"
	"github.com/ma6254/tg_down/internal/utils"
)

// TestDownloadDraftWiring 确认草稿接口的数据库接线与错误映射。
func TestDownloadDraftWiring(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()

	draft, err := getDownloadDraft(ctx)
	if err != nil {
		t.Fatalf("读取空草稿失败: %v", err)
	}
	if draft.Links != "" {
		t.Fatalf("空库应返回默认草稿: %+v", draft)
	}

	saved, err := saveDownloadDraft(ctx, server.DownloadDraftInput{
		Links: "https://t.me/a/1\nhttps://t.me/a/2",
		Cache: json.RawMessage(`{"v":1,"summary":{"total":2,"ok":2}}`),
	})
	if err != nil {
		t.Fatalf("保存草稿失败: %v", err)
	}
	if saved.Links == "" || saved.Cache == nil || saved.UpdatedAt == 0 {
		t.Fatalf("保存结果异常: %+v", saved)
	}

	reloaded, err := getDownloadDraft(ctx)
	if err != nil {
		t.Fatalf("重新读取草稿失败: %v", err)
	}
	if reloaded.Links != saved.Links || reloaded.Cache == nil || reloaded.Cache.Summary.Total != 2 {
		t.Errorf("草稿未持久化: %+v", reloaded)
	}

	// 自动保存只带输入时保留摘要。
	if _, err := saveDownloadDraft(ctx, server.DownloadDraftInput{Links: "short"}); err != nil {
		t.Fatal(err)
	}
	kept, err := getDownloadDraft(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Cache == nil {
		t.Error("省略 cache 时应保留已有摘要")
	}

	// cache: null 清空摘要。
	if _, err := saveDownloadDraft(ctx, server.DownloadDraftInput{
		Links: "short",
		Cache: json.RawMessage("null"),
	}); err != nil {
		t.Fatal(err)
	}
	cleared, err := getDownloadDraft(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Cache != nil {
		t.Errorf("cache: null 应清空摘要: %+v", cleared.Cache)
	}

	tooLong := strings.Repeat("a", downloadinput.MaxLinksBytes+1)
	if _, err := saveDownloadDraft(ctx, server.DownloadDraftInput{Links: tooLong}); !errors.Is(err, server.ErrDownloadDraftInvalid) {
		t.Errorf("超长输入应映射为 ErrDownloadDraftInvalid，实际 %v", err)
	}
}

// TestDownloadHistoryWiring 确认历史记录的增删查与错误映射。
func TestDownloadHistoryWiring(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()

	list, err := listDownloadHistory(ctx, 20)
	if err != nil {
		t.Fatalf("读取空历史失败: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("空库历史应为空: %+v", list.Items)
	}

	cache := json.RawMessage(`{"v":1,"summary":{"total":5,"ok":5},"items":[{"message_id":1,"text":"首条消息","media_kind":"video"}]}`)
	entry, created, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{
		Links: "https://t.me/a/1\nhttps://t.me/a/2",
		Cache: cache,
	})
	if err != nil {
		t.Fatalf("记录历史失败: %v", err)
	}
	if !created || entry.ID == 0 || entry.UseCount != 1 {
		t.Fatalf("记录结果异常: created=%v %+v", created, entry)
	}
	if entry.Links == "" || entry.Cache == nil || !entry.CacheTruncated {
		t.Errorf("详情字段异常: %+v", entry)
	}

	list, err = listDownloadHistory(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != entry.ID {
		t.Fatalf("列表异常: %+v", list.Items)
	}
	if list.Items[0].Summary == nil || list.Items[0].Summary.Total != 5 {
		t.Errorf("列表缺少摘要: %+v", list.Items[0])
	}
	if list.Items[0].Title != "首条消息" {
		t.Errorf("列表标题 = %q，期望 %q", list.Items[0].Title, "首条消息")
	}

	// 同内容再次记录：不新建，次数 +1。
	again, created, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{Links: entry.Links})
	if err != nil {
		t.Fatal(err)
	}
	if created || again.ID != entry.ID || again.UseCount != 2 {
		t.Errorf("重复记录异常: created=%v %+v", created, again)
	}

	got, err := getDownloadHistory(ctx, entry.ID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if got.Links != entry.Links {
		t.Errorf("详情 links = %q", got.Links)
	}

	if _, err := getDownloadHistory(ctx, 9999); !errors.Is(err, server.ErrDownloadHistoryNotFound) {
		t.Errorf("不存在应映射为 ErrDownloadHistoryNotFound，实际 %v", err)
	}

	tooLong := strings.Repeat("a", downloadinput.MaxLinksBytes+1)
	if _, _, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{Links: tooLong}); !errors.Is(err, server.ErrDownloadHistoryInvalid) {
		t.Errorf("超长输入应映射为 ErrDownloadHistoryInvalid，实际 %v", err)
	}

	if err := deleteDownloadHistory(ctx, entry.ID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if err := deleteDownloadHistory(ctx, entry.ID); !errors.Is(err, server.ErrDownloadHistoryNotFound) {
		t.Errorf("重复删除应映射为 ErrDownloadHistoryNotFound，实际 %v", err)
	}

	// 再记录两条后清空，确认 removed 计数。
	for _, link := range []string{"https://t.me/b/1", "https://t.me/b/2"} {
		if _, _, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{Links: link}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := clearDownloadHistory(ctx)
	if err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	if removed != 2 {
		t.Errorf("清空条数 = %d，期望 2", removed)
	}
}

// TestEnsureDownloadSettings 确认旧草稿的两个开关会迁移到系统配置且不覆盖已有值。
func TestEnsureDownloadSettings(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if _, err := db.SaveUIDraft(downloadinput.DraftKey,
		`{"links":"x","expand_text_links":true,"overwrite":true}`); err != nil {
		t.Fatalf("写入旧草稿失败: %v", err)
	}
	_ = db.Close()

	if err := ensureDownloadSettings(2); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	db, err = utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	settings := config.NewStore(db)
	for _, key := range []string{config.KeyDownloadOverwrite, config.KeyDownloadExpandTextLinks} {
		value, ok, err := settings.Get(key)
		if err != nil || !ok || value != "true" {
			t.Errorf("迁移后 %s = %q, %v, %v，期望 true", key, value, ok, err)
		}
	}
	// 自动解析没有旧字段可继承，默认开启；并发数取启动参数。
	if value, ok, err := settings.Get(config.KeyDownloadAutoPreview); err != nil || !ok || value != "true" {
		t.Errorf("download_auto_preview = %q, %v, %v，期望 true", value, ok, err)
	}
	for key, expected := range map[string]string{
		config.KeyDownloadWorkers: "2",
		config.KeyDownloadThreads: strconv.Itoa(manager.DefaultThreads),
	} {
		value, ok, err := settings.Get(key)
		if err != nil || !ok || value != expected {
			t.Errorf("%s = %q, %v, %v，期望 %q", key, value, ok, err, expected)
		}
	}

	// 已有配置（用户后来改过）时不再用旧草稿覆盖。
	boolKeys := []string{
		config.KeyDownloadOverwrite,
		config.KeyDownloadExpandTextLinks,
		config.KeyDownloadAutoPreview,
	}
	for _, key := range boolKeys {
		if err := settings.Set(key, "false"); err != nil {
			t.Fatal(err)
		}
	}
	intKeys := map[string]string{
		config.KeyDownloadWorkers: "1",
		config.KeyDownloadThreads: "2",
	}
	for key, value := range intKeys {
		if err := settings.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	if err := ensureDownloadSettings(2); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	db, err = utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings = config.NewStore(db)
	for _, key := range boolKeys {
		value, _, err := settings.Get(key)
		if err != nil || value != "false" {
			t.Errorf("已有配置 %s 被覆盖: %q, %v", key, value, err)
		}
	}
	for key, expected := range intKeys {
		value, _, err := settings.Get(key)
		if err != nil || value != expected {
			t.Errorf("已有配置 %s 被覆盖: %q, %v，期望 %q", key, value, err, expected)
		}
	}
}

// TestEnsureDownloadSettingsDefault 确认没有旧草稿时也会补写默认值。
func TestEnsureDownloadSettingsDefault(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())

	if err := ensureDownloadSettings(3); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	db, err := utils.OpenDatabase()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	settings := config.NewStore(db)
	want := map[string]string{
		config.KeyDownloadOverwrite:       "false",
		config.KeyDownloadExpandTextLinks: "false",
		config.KeyDownloadAutoPreview:     "true",
		config.KeyDownloadWorkers:         "3",
		config.KeyDownloadThreads:         strconv.Itoa(manager.DefaultThreads),
		config.KeyWebTheme:                config.DefaultWebTheme,
		config.KeyWebTitleSpeed:           config.DefaultWebTitleSpeed,
	}
	for key, expected := range want {
		value, ok, err := settings.Get(key)
		if err != nil || !ok || value != expected {
			t.Errorf("%s 默认值 = %q, %v, %v，期望 %q", key, value, ok, err, expected)
		}
	}
}

// TestDownloadHistoryCoverWiring 确认封面从 thumbnail/ 磁盘缓存读取。
func TestDownloadHistoryCoverWiring(t *testing.T) {
	t.Setenv("TG_DOWN_HOME", t.TempDir())
	ctx := context.Background()

	noCover, _, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{Links: "https://t.me/cover/1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getDownloadHistoryCover(ctx, noCover.ID); !errors.Is(err, server.ErrThumbnailNotFound) {
		t.Errorf("没有封面键应返回 ErrThumbnailNotFound，实际 %v", err)
	}

	key := thumbcache.Key(mtproto.Thumb{Kind: mtproto.ThumbKindPhoto, FileID: 4242, SizeType: "m"})
	if err := thumbcache.Put(key, []byte("cover-jpeg")); err != nil {
		t.Fatal(err)
	}
	cached, _, err := recordDownloadHistory(ctx, server.DownloadHistoryInput{
		Links: "https://t.me/cover/2",
		Cache: json.RawMessage(fmt.Sprintf(
			`{"v":1,"summary":{"total":1,"ok":1},"items":[{"message_id":1,"thumb_key":%q}]}`, key,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	cover, err := getDownloadHistoryCover(ctx, cached.ID)
	if err != nil {
		t.Fatalf("读取封面失败: %v", err)
	}
	if string(cover.Data) != "cover-jpeg" || cover.ContentType != "image/jpeg" {
		t.Errorf("封面 = %q / %q", cover.Data, cover.ContentType)
	}

	// 记录还在但缓存文件被删：按「没有封面」返回 404。
	path, err := thumbcache.Path(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := getDownloadHistoryCover(ctx, cached.ID); !errors.Is(err, server.ErrThumbnailNotFound) {
		t.Errorf("缓存文件缺失应返回 ErrThumbnailNotFound，实际 %v", err)
	}
}
