package server

import (
	"reflect"
	"testing"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// TestPreviewItemsAlbum 确认相册媒体会映射为 medias，并带单条消息链接。
func TestPreviewItemsAlbum(t *testing.T) {
	results := []mtproto.LinkResult{{
		Target:    link.Target{Raw: "https://t.me/AV_mys/8074"},
		Chat:      mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 1, Title: "相册", Username: "AV_mys"},
		MessageID: 8074,
		Media: &mtproto.Media{
			MessageID: 8074, Link: "https://t.me/AV_mys/8074",
			Kind: mtproto.MediaKindPhoto, FileName: "photo_8074.jpg", Size: 100,
		},
		Medias: []*mtproto.Media{
			{MessageID: 8074, Link: "https://t.me/AV_mys/8074", Kind: mtproto.MediaKindPhoto, FileName: "photo_8074.jpg", Size: 100},
			{MessageID: 8075, Link: "https://t.me/AV_mys/8075", Kind: mtproto.MediaKindPhoto, FileName: "photo_8075.jpg", Size: 200},
			{MessageID: 8076, Link: "https://t.me/AV_mys/8076", Kind: mtproto.MediaKindVideo, FileName: "clip.mp4", Size: 300},
		},
	}}

	items := NewPreviewItems(results, false, mtproto.Session{})
	if len(items) != 1 {
		t.Fatalf("items 数量 = %d", len(items))
	}
	if items[0].Media == nil || items[0].Media.MessageID != 8074 {
		t.Fatalf("主媒体 = %+v", items[0].Media)
	}
	if len(items[0].Medias) != 3 {
		t.Fatalf("相册媒体数量 = %d，期望 3", len(items[0].Medias))
	}
	if items[0].Medias[2].Link != "https://t.me/AV_mys/8076" {
		t.Errorf("第 3 条链接 = %q", items[0].Medias[2].Link)
	}
}

// TestPreviewItemsEntities 确认正文实体会原样映射到接口结构。
func TestPreviewItemsEntities(t *testing.T) {
	results := []mtproto.LinkResult{{
		Target:    link.Target{Raw: "https://t.me/a/1", Username: "a", MessageID: 1},
		Chat:      mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 1, Title: "频道"},
		MessageID: 1,
		Text:      "看这段代码",
		Entities: []mtproto.MessageEntity{
			{Type: "bold", Offset: 0, Length: 1},
			{Type: "pre", Offset: 2, Length: 4, Language: "go"},
			{Type: "custom_emoji", Offset: 1, Length: 1, DocumentID: 42},
		},
	}}

	items := NewPreviewItems(results, false, mtproto.Session{})
	if len(items) != 1 {
		t.Fatalf("items 数量 = %d，期望 1", len(items))
	}

	want := []PreviewEntity{
		{Type: "bold", Offset: 0, Length: 1},
		{Type: "pre", Offset: 2, Length: 4, Language: "go"},
		{Type: "custom_emoji", Offset: 1, Length: 1, DocumentID: 42},
	}
	if !reflect.DeepEqual(items[0].Entities, want) {
		t.Errorf("entities = %+v，期望 %+v", items[0].Entities, want)
	}
}

// TestPreviewItemsPreviewURL 确认封面与正文大图各自拿到独立的 token 地址。
func TestPreviewItemsPreviewURL(t *testing.T) {
	results := []mtproto.LinkResult{{
		Target:    link.Target{Raw: "https://t.me/a/1", Username: "a", MessageID: 1},
		Chat:      mtproto.Peer{Kind: mtproto.PeerKindChannel, ID: 1, Title: "频道"},
		MessageID: 1,
		Media: &mtproto.Media{
			Kind:     mtproto.MediaKindPhoto,
			FileName: "photo_1.jpg",
			Size:     1024,
			Thumb:    &mtproto.Thumb{Kind: mtproto.ThumbKindPhoto, SizeType: "m", Width: 320, Height: 240},
			Preview:  &mtproto.Thumb{Kind: mtproto.ThumbKindPhoto, SizeType: "x", Width: 800, Height: 600},
		},
	}}

	items := NewPreviewItems(results, false, mtproto.Session{})
	if len(items) != 1 || items[0].Media == nil {
		t.Fatalf("items = %+v", items)
	}
	media := items[0].Media
	if media.ThumbnailURL == "" || media.PreviewURL == "" {
		t.Fatalf("封面或大图地址为空: %+v", media)
	}
	if media.ThumbnailURL == media.PreviewURL {
		t.Errorf("封面与大图不应共用同一个 token: %q", media.PreviewURL)
	}
}

// TestPreviewResponseSummary 确认统计口径：成功、失败、无媒体、展开。
func TestPreviewResponseSummary(t *testing.T) {
	items := []PreviewItem{
		{Link: "a", Error: "boom"},
		{Link: "b", Media: &PreviewMedia{}},
		{Link: "c"},
		{Link: "d", Expanded: true, Media: &PreviewMedia{}},
	}
	resp := NewPreviewResponse(items, 3)
	if resp.Summary.Total != 4 || resp.Summary.OK != 3 || resp.Summary.Failed != 1 {
		t.Errorf("summary = %+v", resp.Summary)
	}
	if resp.Summary.NoMedia != 1 {
		t.Errorf("no_media = %d，期望 1", resp.Summary.NoMedia)
	}
	if resp.Summary.Expanded != 1 {
		t.Errorf("expanded = %d，期望 1", resp.Summary.Expanded)
	}
	if resp.Summary.SkippedLinks != 3 {
		t.Errorf("skipped_links = %d，期望 3", resp.Summary.SkippedLinks)
	}
}
