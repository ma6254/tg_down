package mtproto

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// newMessage 构造一条带媒体的消息，SetMedia 会同时设置条件字段的 flag。
func newMessage(media tg.MessageMediaClass) *tg.Message {
	msg := &tg.Message{ID: 8888, Date: 1763500000, Message: "第 1 集"}
	msg.SetMedia(media)
	return msg
}

// TestMediaFromDocument 覆盖视频文档与文件名的提取。
func TestMediaFromDocument(t *testing.T) {
	doc := &tg.Document{
		ID:            42,
		AccessHash:    4242,
		FileReference: []byte{1, 2, 3},
		Size:          734003200,
		MimeType:      "video/mp4",
		DCID:          4,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "ep01.mp4"},
			&tg.DocumentAttributeVideo{Duration: 1425, W: 1920, H: 1080},
		},
	}
	msg := newMessage(&tg.MessageMediaDocument{Document: doc})
	msg.SetGroupedID(777)

	media, err := mediaFromMessage(msg)
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}

	if media.Kind != MediaKindVideo {
		t.Errorf("Kind = %q，期望 %q", media.Kind, MediaKindVideo)
	}
	if media.FileName != "ep01.mp4" {
		t.Errorf("FileName = %q", media.FileName)
	}
	if media.Size != 734003200 {
		t.Errorf("Size = %d", media.Size)
	}
	if media.MIME != "video/mp4" {
		t.Errorf("MIME = %q", media.MIME)
	}
	if media.DCID != 4 || media.FileID != 42 || media.AccessHash != 4242 {
		t.Errorf("定位信息不正确: %+v", media)
	}
	if media.Duration != 1425 || media.Width != 1920 || media.Height != 1080 {
		t.Errorf("音视频属性不正确: %+v", media)
	}
	if media.GroupedID != 777 {
		t.Errorf("GroupedID = %d，期望 777", media.GroupedID)
	}
	if len(media.FileReference) != 3 {
		t.Errorf("FileReference = %v", media.FileReference)
	}
	if len(media.Caption) == 0 {
		t.Error("Caption 为空")
	}
}

// TestMediaFromDocumentWithoutName 覆盖没有文件名时按 MIME 补扩展名。
func TestMediaFromDocumentWithoutName(t *testing.T) {
	doc := &tg.Document{
		ID:       7,
		Size:     1024,
		MimeType: "application/pdf",
		DCID:     2,
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaDocument{Document: doc}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Kind != MediaKindDocument {
		t.Errorf("Kind = %q，期望 %q", media.Kind, MediaKindDocument)
	}
	if media.FileName != "document_7.pdf" {
		t.Errorf("FileName = %q，期望 document_7.pdf", media.FileName)
	}
}

// TestMediaFromVoice 覆盖语音消息被归为音频。
func TestMediaFromVoice(t *testing.T) {
	doc := &tg.Document{
		ID:       9,
		Size:     4096,
		MimeType: "audio/ogg",
		DCID:     2,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Voice: true, Duration: 12},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaDocument{Document: doc, Voice: true}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Kind != MediaKindAudio {
		t.Errorf("Kind = %q，期望 %q", media.Kind, MediaKindAudio)
	}
	if media.Duration != 12 {
		t.Errorf("Duration = %d", media.Duration)
	}
}

// TestMediaFromPhoto 覆盖照片取最大尺寸。
func TestMediaFromPhoto(t *testing.T) {
	photo := &tg.Photo{
		ID:         99,
		AccessHash: 9900,
		DCID:       2,
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 100, H: 100, Size: 1000},
			&tg.PhotoSize{Type: "y", W: 1280, H: 720, Size: 50000},
			&tg.PhotoSizeProgressive{Type: "x", W: 800, H: 600, Sizes: []int{2000, 30000}},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaPhoto{Photo: photo}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}

	if media.Kind != MediaKindPhoto {
		t.Errorf("Kind = %q，期望 %q", media.Kind, MediaKindPhoto)
	}
	if media.Size != 50000 {
		t.Errorf("Size = %d，期望 50000", media.Size)
	}
	if media.Width != 1280 || media.Height != 720 {
		t.Errorf("尺寸 = %d×%d", media.Width, media.Height)
	}
	if media.FileName != "photo_99.jpg" {
		t.Errorf("FileName = %q", media.FileName)
	}
	if media.MIME != "image/jpeg" {
		t.Errorf("MIME = %q", media.MIME)
	}
	if media.PhotoSizeType != "y" {
		t.Errorf("PhotoSizeType = %q，期望 y（下载照片需要尺寸类型）", media.PhotoSizeType)
	}
}

// TestAlbumMedias 确认相册按消息 ID 排序、过滤不同分组，并补上单条链接。
func TestAlbumMedias(t *testing.T) {
	const groupedID = int64(777)
	peer := Peer{Kind: PeerKindChannel, ID: 123456, Username: "AV_mys"}

	messages := []*tg.Message{
		albumPhotoMessage(8076, groupedID),
		albumPhotoMessage(8074, groupedID),
		albumPhotoMessage(8077, 999), // 不同分组，应排除
		albumPhotoMessage(8075, groupedID),
	}

	medias := albumMedias(peer, messages, groupedID, 2<<30)
	if len(medias) != 3 {
		t.Fatalf("媒体数量 = %d，期望 3", len(medias))
	}
	for i, wantID := range []int{8074, 8075, 8076} {
		if medias[i].MessageID != wantID {
			t.Errorf("第 %d 个消息 ID = %d，期望 %d", i+1, medias[i].MessageID, wantID)
		}
	}
	if medias[0].Link != "https://t.me/AV_mys/8074" {
		t.Errorf("Link = %q", medias[0].Link)
	}
	if !medias[0].Downloadable {
		t.Error("小于上限时应可下载")
	}
}

// TestPeerMessageURL 覆盖公开频道与私有频道的链接拼法。
func TestPeerMessageURL(t *testing.T) {
	public := Peer{Kind: PeerKindChannel, ID: 1, Username: "av_mys"}
	if got := public.MessageURL(8074); got != "https://t.me/av_mys/8074" {
		t.Errorf("公开频道链接 = %q", got)
	}
	private := Peer{Kind: PeerKindChannel, ID: 3713824040}
	if got := private.MessageURL(8074); got != "https://t.me/c/3713824040/8074" {
		t.Errorf("私有频道链接 = %q", got)
	}
	if got := (Peer{Kind: PeerKindUser, ID: 1}).MessageURL(1); got != "" {
		t.Errorf("私聊不应拼出链接，实际 %q", got)
	}
}

// albumPhotoMessage 构造一条相册照片消息。
func albumPhotoMessage(id int, groupedID int64) *tg.Message {
	msg := &tg.Message{ID: id, Message: "相册"}
	msg.SetMedia(&tg.MessageMediaPhoto{Photo: &tg.Photo{
		ID:         int64(id),
		AccessHash: int64(id) * 10,
		DCID:       2,
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: id},
		},
	}})
	msg.SetGroupedID(groupedID)
	return msg
}

// TestMediaFromMessageEmpty 覆盖没有媒体的消息。
func TestMediaFromMessageEmpty(t *testing.T) {
	msg := &tg.Message{ID: 1}
	if _, err := mediaFromMessage(msg); !errors.Is(err, ErrNoMedia) {
		t.Fatalf("期望 ErrNoMedia，实际 %v", err)
	}

	msg.SetMedia(&tg.MessageMediaEmpty{})
	if _, err := mediaFromMessage(msg); !errors.Is(err, ErrNoMedia) {
		t.Fatalf("空媒体期望 ErrNoMedia，实际 %v", err)
	}
}

// TestMediaFromUnsupported 覆盖暂不支持的媒体类型。
func TestMediaFromUnsupported(t *testing.T) {
	msg := newMessage(&tg.MessageMediaUnsupported{})
	if _, err := mediaFromMessage(msg); err == nil {
		t.Fatal("期望报错，实际通过")
	}
}

// TestLargestPhotoSize 覆盖只有缓存尺寸时的兜底。
func TestLargestPhotoSize(t *testing.T) {
	size, width, height, sizeType := largestPhotoSize([]tg.PhotoSizeClass{
		&tg.PhotoCachedSize{Type: "x", W: 90, H: 60, Bytes: []byte{1, 2, 3, 4}},
	})
	if size != 4 || width != 90 || height != 60 || sizeType != "x" {
		t.Fatalf("largestPhotoSize = (%d, %d, %d, %q)", size, width, height, sizeType)
	}
}

// TestDocumentThumb 确认视频文档会挑出 m 尺寸作为封面。
func TestDocumentThumb(t *testing.T) {
	doc := &tg.Document{
		ID:         42,
		AccessHash: 4242,
		DCID:       4,
		MimeType:   "video/mp4",
		Thumbs: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 100, H: 56, Size: 1000},
			&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 6000},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaDocument{Document: doc}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Thumb == nil {
		t.Fatal("没有提取到缩略图")
	}
	if media.Thumb.Kind != ThumbKindDocument {
		t.Errorf("Kind = %q", media.Thumb.Kind)
	}
	if media.Thumb.SizeType != "m" || media.Thumb.Width != 320 || media.Thumb.Height != 180 {
		t.Errorf("Thumb = %+v", media.Thumb)
	}
	if media.Thumb.FileID != doc.ID || media.Thumb.AccessHash != doc.AccessHash {
		t.Errorf("缩略图定位信息不正确: %+v", media.Thumb)
	}
}

// TestDocumentThumbMissing 没有缩略图时保持 nil。
func TestDocumentThumbMissing(t *testing.T) {
	doc := &tg.Document{ID: 7, MimeType: "application/pdf"}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaDocument{Document: doc}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Thumb != nil {
		t.Errorf("不应有缩略图: %+v", media.Thumb)
	}
}

// TestPhotoThumb 确认照片优先 s 尺寸。
func TestPhotoThumb(t *testing.T) {
	photo := &tg.Photo{
		ID: 99,
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 100, H: 80, Size: 1000},
			&tg.PhotoSize{Type: "x", W: 800, H: 640, Size: 50000},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaPhoto{Photo: photo}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Thumb == nil || media.Thumb.Kind != ThumbKindPhoto {
		t.Fatalf("Thumb = %+v", media.Thumb)
	}
	if media.Thumb.SizeType != "s" {
		t.Errorf("SizeType = %q，期望 s", media.Thumb.SizeType)
	}
}

// TestPhotoPreview 确认正文大图优先 x（800px）而不是列表封面用的 s。
func TestPhotoPreview(t *testing.T) {
	photo := &tg.Photo{
		ID:         99,
		AccessHash: 9900,
		DCID:       2,
		Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "s", W: 100, H: 80, Size: 1000},
			&tg.PhotoSize{Type: "x", W: 800, H: 640, Size: 50000},
			&tg.PhotoSize{Type: "y", W: 1280, H: 1024, Size: 90000},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaPhoto{Photo: photo}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Preview == nil {
		t.Fatal("没有提取到正文大图")
	}
	if media.Preview.Kind != ThumbKindPhoto || media.Preview.SizeType != "x" {
		t.Errorf("Preview = %+v，期望 photo/x", media.Preview)
	}
	if media.Preview.Width != 800 || media.Preview.Height != 640 {
		t.Errorf("Preview 尺寸 = %d×%d", media.Preview.Width, media.Preview.Height)
	}
}

// TestDocumentPreview 确认文档类媒体的正文大图优先 y（1280px）。
func TestDocumentPreview(t *testing.T) {
	doc := &tg.Document{
		ID:         42,
		AccessHash: 4242,
		DCID:       4,
		MimeType:   "video/mp4",
		Thumbs: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "m", W: 320, H: 180, Size: 6000},
			&tg.PhotoSize{Type: "y", W: 1280, H: 720, Size: 80000},
		},
	}
	media, err := mediaFromMessage(newMessage(&tg.MessageMediaDocument{Document: doc}))
	if err != nil {
		t.Fatalf("mediaFromMessage 报错: %v", err)
	}
	if media.Preview == nil || media.Preview.SizeType != "y" {
		t.Fatalf("Preview = %+v，期望 y", media.Preview)
	}
	if media.Preview.Kind != ThumbKindDocument {
		t.Errorf("Preview.Kind = %q", media.Preview.Kind)
	}
}

// TestPickPreviewSizeFallback 没有优先级尺寸时退到面积最大的一张。
func TestPickPreviewSizeFallback(t *testing.T) {
	sizeType, width, height := pickPreviewSize([]tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "q", W: 200, H: 100, Size: 1000},
		&tg.PhotoSize{Type: "z", W: 300, H: 300, Size: 5000},
	})
	if sizeType != "z" || width != 300 || height != 300 {
		t.Fatalf("pickPreviewSize = (%q, %d, %d)", sizeType, width, height)
	}
}

// TestPickThumbSizeFallback 没有 m/s 时退到面积不超过 400×400 的最大图。
func TestPickThumbSizeFallback(t *testing.T) {
	sizeType, width, height := pickThumbSize([]tg.PhotoSizeClass{
		&tg.PhotoSizeProgressive{Type: "x", W: 800, H: 600, Sizes: []int{1000, 5000}},
		&tg.PhotoSizeProgressive{Type: "y", W: 320, H: 240, Sizes: []int{1000, 5000}},
	})
	if sizeType != "y" || width != 320 || height != 240 {
		t.Fatalf("pickThumbSize = (%q, %d, %d)", sizeType, width, height)
	}
}
