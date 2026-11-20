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
	size, width, height := largestPhotoSize([]tg.PhotoSizeClass{
		&tg.PhotoCachedSize{Type: "x", W: 90, H: 60, Bytes: []byte{1, 2, 3, 4}},
	})
	if size != 4 || width != 90 || height != 60 {
		t.Fatalf("largestPhotoSize = (%d, %d, %d)", size, width, height)
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
