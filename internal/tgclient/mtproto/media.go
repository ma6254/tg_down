package mtproto

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/tg"
)

// MediaKind 是媒体的粗略分类，供展示与筛选使用。
type MediaKind string

const (
	MediaKindPhoto    MediaKind = "photo"
	MediaKindVideo    MediaKind = "video"
	MediaKindAudio    MediaKind = "audio"
	MediaKindDocument MediaKind = "document"
)

// 单文件大小上限：普通账号 2 GB，Premium 账号 4 GB。
const (
	MaxFileSize        int64 = 2 << 30
	MaxFileSizePremium int64 = 4 << 30
)

// ErrNoMedia 表示该消息没有可下载的媒体。
var ErrNoMedia = errors.New("该消息不包含可下载的媒体")

// 缩略图所属的媒体种类。
const (
	ThumbKindPhoto    = "photo"
	ThumbKindDocument = "document"
)

// Thumb 描述媒体的缩略图定位信息（Telegram 图床位置，不是可直接访问的 URL）。
//
// FileReference 会过期，缓存时间不宜过长；下载失败时前端退回类型图标。
type Thumb struct {
	Kind          string // photo | document
	FileID        int64
	AccessHash    int64
	FileReference []byte
	SizeType      string // Telegram 缩略图类型，如 s / m / x
	DCID          int
	Width         int
	Height        int
}

// Media 是一条消息里的可下载媒体及其定位信息。
//
// FileReference 会过期，排队下载时应只保存 MessageID 等身份信息，
// 下载前重新取一次消息拿新的引用。
type Media struct {
	MessageID int
	Date      time.Time
	Caption   string
	GroupedID int64 // 相册分组 ID，0 表示不属于相册
	// Link 是这条消息的链接（相册里每张图可单独下载）；
	// 私聊等无法拼出链接的场景为空，调用方退回原始输入链接。
	Link string

	Kind          MediaKind
	FileName      string
	Size          int64
	MIME          string
	DCID          int
	FileID        int64
	AccessHash    int64
	FileReference []byte
	// PhotoSizeType 是照片完整文件对应的尺寸类型（如 x/y/w）；
	// 下载照片时 InputPhotoFileLocation 必须带它，空串会被 Telegram 拒绝。
	PhotoSizeType string
	Duration      int // 秒；非音视频为 0
	Width         int
	Height        int

	// Thumb 是列表用的缩略图；没有可用缩略图时为 nil（如音频、文档）。
	Thumb *Thumb

	// Preview 是正文大图用的更大尺寸；没有时为 nil，前端退回 Thumb。
	Preview *Thumb

	// Downloadable 表示大小没有超过当前账号的单文件上限，MaxSize 是该上限。
	Downloadable bool
	MaxSize      int64
}

// mediaFromMessage 从消息里提取媒体信息；没有媒体时返回 ErrNoMedia。
func mediaFromMessage(msg *tg.Message) (*Media, error) {
	if msg == nil {
		return nil, ErrNoMedia
	}
	raw, ok := msg.GetMedia()
	if !ok {
		return nil, ErrNoMedia
	}

	base := Media{
		MessageID: msg.ID,
		Date:      unixTime(msg.Date),
		Caption:   msg.Message,
		GroupedID: groupedID(msg),
	}

	switch m := raw.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok || doc == nil {
			return nil, fmt.Errorf("文档已失效或不可用")
		}
		return mediaFromDocument(base, doc, m)
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok || photo == nil {
			return nil, fmt.Errorf("照片已失效或不可用")
		}
		return mediaFromPhoto(base, photo)
	case *tg.MessageMediaEmpty:
		return nil, ErrNoMedia
	default:
		return nil, fmt.Errorf("暂不支持该媒体类型（%T）", raw)
	}
}

// mediaFromDocument 提取文档类媒体（视频、音频、普通文件）。
func mediaFromDocument(base Media, doc *tg.Document, media *tg.MessageMediaDocument) (*Media, error) {
	base.Kind = MediaKindDocument
	base.FileID = doc.ID
	base.AccessHash = doc.AccessHash
	base.FileReference = doc.FileReference
	base.Size = doc.Size
	base.MIME = doc.MimeType
	base.DCID = doc.DCID

	var (
		name     string
		hasVideo bool
		hasAudio bool
	)
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			if strings.TrimSpace(a.FileName) != "" {
				name = a.FileName
			}
		case *tg.DocumentAttributeVideo:
			hasVideo = true
			base.Duration = int(a.Duration)
			base.Width, base.Height = a.W, a.H
		case *tg.DocumentAttributeAudio:
			hasAudio = true
			base.Duration = a.Duration
		}
	}

	// 媒体上的标记比文档属性更权威（例如语音、圆视频）。
	if hasVideo || media.Video || media.Round {
		base.Kind = MediaKindVideo
	} else if hasAudio || media.Voice {
		base.Kind = MediaKindAudio
	}

	if name == "" {
		name = fmt.Sprintf("document_%d%s", doc.ID, extensionFor(doc.MimeType))
	}
	base.FileName = name
	base.Thumb = documentThumb(doc)
	base.Preview = documentPreview(doc)
	return &base, nil
}

// mediaFromPhoto 提取照片：取最大尺寸作为下载目标。
func mediaFromPhoto(base Media, photo *tg.Photo) (*Media, error) {
	size, width, height, sizeType := largestPhotoSize(photo.Sizes)

	base.Kind = MediaKindPhoto
	base.FileID = photo.ID
	base.AccessHash = photo.AccessHash
	base.FileReference = photo.FileReference
	base.Size = size
	base.Width, base.Height = width, height
	// 下载完整照片时 InputPhotoFileLocation 必须带尺寸类型，空串会被 Telegram
	// 当作无效引用（FILE_REFERENCE_EXPIRED）。
	base.PhotoSizeType = sizeType
	base.DCID = photo.DCID
	base.MIME = "image/jpeg"
	base.FileName = fmt.Sprintf("photo_%d.jpg", photo.ID)
	base.Thumb = photoThumb(photo)
	base.Preview = photoPreview(photo)
	return &base, nil
}

// documentThumb 从文档的缩略图列表里挑一张列表封面；没有时返回 nil。
func documentThumb(doc *tg.Document) *Thumb {
	sizeType, width, height := pickThumbSize(doc.Thumbs)
	if sizeType == "" {
		return nil
	}
	return &Thumb{
		Kind:          ThumbKindDocument,
		FileID:        doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		SizeType:      sizeType,
		DCID:          doc.DCID,
		Width:         width,
		Height:        height,
	}
}

// photoThumb 从照片尺寸里挑一张小图作为列表封面。
func photoThumb(photo *tg.Photo) *Thumb {
	sizeType, width, height := pickThumbSize(photo.Sizes)
	if sizeType == "" {
		return nil
	}
	return &Thumb{
		Kind:          ThumbKindPhoto,
		FileID:        photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
		SizeType:      sizeType,
		DCID:          photo.DCID,
		Width:         width,
		Height:        height,
	}
}

// photoPreview 挑一张更大的图给正文展示，没有合适尺寸时返回 nil。
func photoPreview(photo *tg.Photo) *Thumb {
	sizeType, width, height := pickPreviewSize(photo.Sizes)
	if sizeType == "" {
		return nil
	}
	return &Thumb{
		Kind:          ThumbKindPhoto,
		FileID:        photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
		SizeType:      sizeType,
		DCID:          photo.DCID,
		Width:         width,
		Height:        height,
	}
}

// documentPreview 挑一张更大的图给正文展示，没有合适尺寸时返回 nil。
func documentPreview(doc *tg.Document) *Thumb {
	sizeType, width, height := pickPreviewSize(doc.Thumbs)
	if sizeType == "" {
		return nil
	}
	return &Thumb{
		Kind:          ThumbKindDocument,
		FileID:        doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		SizeType:      sizeType,
		DCID:          doc.DCID,
		Width:         width,
		Height:        height,
	}
}

// previewPreference 是正文大图的尺寸优先级：约 800px 宽足够清晰，又不会太重。
var previewPreference = []string{"x", "y", "m", "s", "w"}

// pickPreviewSize 按优先级挑正文大图尺寸；都没有时取面积最大的一个。
func pickPreviewSize(sizes []tg.PhotoSizeClass) (sizeType string, width, height int) {
	type dims struct{ w, h int }
	available := make(map[string]dims, len(sizes))
	var (
		largestType string
		largestW    int
		largestH    int
		largestArea int
	)

	for _, item := range sizes {
		typ, w, h, ok := photoSizeInfo(item)
		if !ok || typ == "" {
			continue
		}
		available[typ] = dims{w: w, h: h}
		if area := w * h; area > largestArea {
			largestType, largestW, largestH, largestArea = typ, w, h, area
		}
	}

	for _, want := range previewPreference {
		if size, ok := available[want]; ok {
			return want, size.w, size.h
		}
	}
	return largestType, largestW, largestH
}

// photoSizeInfo 取尺寸项的类型与宽高；不可下载的尺寸（如 stripped）返回 false。
func photoSizeInfo(item tg.PhotoSizeClass) (typ string, width, height int, ok bool) {
	switch s := item.(type) {
	case *tg.PhotoSize:
		return s.Type, s.W, s.H, true
	case *tg.PhotoSizeProgressive:
		return s.Type, s.W, s.H, true
	case *tg.PhotoCachedSize:
		return s.Type, s.W, s.H, true
	}
	return "", 0, 0, false
}

// pickThumbSize 选择列表封面尺寸：优先 Telegram 的 m（约 320px），
// 其次 s，再退到面积不超过 400×400 的最大图。
func pickThumbSize(sizes []tg.PhotoSizeClass) (sizeType string, width, height int) {
	var (
		fallbackType string
		fallbackW    int
		fallbackH    int
		bestArea     int
		bestType     string
		bestW        int
		bestH        int
	)

	for _, item := range sizes {
		var (
			typ string
			w   int
			h   int
		)
		switch s := item.(type) {
		case *tg.PhotoSize:
			typ, w, h = s.Type, s.W, s.H
		case *tg.PhotoSizeProgressive:
			typ, w, h = s.Type, s.W, s.H
		case *tg.PhotoCachedSize:
			typ, w, h = s.Type, s.W, s.H
		default:
			// PhotoStrippedSize 等没有可下载的位置，跳过。
			continue
		}
		if typ == "" {
			continue
		}

		switch typ {
		case "m":
			return typ, w, h
		case "s":
			if fallbackType == "" {
				fallbackType, fallbackW, fallbackH = typ, w, h
			}
		}

		if area := w * h; area > bestArea && (w <= 400 || h <= 400) {
			bestType, bestW, bestH, bestArea = typ, w, h, area
		}
	}

	if fallbackType != "" {
		return fallbackType, fallbackW, fallbackH
	}
	return bestType, bestW, bestH
}

// largestPhotoSize 返回最大尺寸的字节数、宽高与尺寸类型。
//
// 有的照片只提供 progressive 尺寸，其 Sizes 是渐进前缀的累计大小，
// 最后一项才是完整文件；缓存尺寸则用字节长度估算。
// 尺寸类型用于 InputPhotoFileLocation，空串会被 Telegram 拒绝。
func largestPhotoSize(sizes []tg.PhotoSizeClass) (size int64, width, height int, sizeType string) {
	var (
		bestSize int64
		bestW    int
		bestH    int
		bestType string
	)
	for _, item := range sizes {
		var (
			typ     string
			curSize int64
			curW    int
			curH    int
		)
		switch s := item.(type) {
		case *tg.PhotoSize:
			typ, curSize, curW, curH = s.Type, int64(s.Size), s.W, s.H
		case *tg.PhotoSizeProgressive:
			typ, curW, curH = s.Type, s.W, s.H
			if len(s.Sizes) > 0 {
				curSize = int64(s.Sizes[len(s.Sizes)-1])
			}
		case *tg.PhotoCachedSize:
			typ, curSize, curW, curH = s.Type, int64(len(s.Bytes)), s.W, s.H
		}
		if curSize > bestSize {
			bestSize, bestW, bestH, bestType = curSize, curW, curH, typ
		}
	}
	return bestSize, bestW, bestH, bestType
}

// fileLocation 返回完整文件的下载位置。
func (m *Media) fileLocation() (tg.InputFileLocationClass, error) {
	if m == nil {
		return nil, errors.New("缺少媒体信息")
	}
	if len(m.FileReference) == 0 {
		return nil, errors.New("缺少文件引用，请重新解析链接")
	}

	if m.Kind == MediaKindPhoto {
		return &tg.InputPhotoFileLocation{
			ID:            m.FileID,
			AccessHash:    m.AccessHash,
			FileReference: m.FileReference,
			ThumbSize:     m.PhotoSizeType,
		}, nil
	}
	return &tg.InputDocumentFileLocation{
		ID:            m.FileID,
		AccessHash:    m.AccessHash,
		FileReference: m.FileReference,
	}, nil
}

// groupedID 返回相册分组 ID，不属于相册时为 0。
func groupedID(msg *tg.Message) int64 {
	if id, ok := msg.GetGroupedID(); ok {
		return id
	}
	return 0
}

// mimeExtensions 覆盖常见类型；没有文件名时用它补扩展名。
var mimeExtensions = map[string]string{
	"video/mp4":               ".mp4",
	"video/webm":              ".webm",
	"video/quicktime":         ".mov",
	"audio/mpeg":              ".mp3",
	"audio/ogg":               ".ogg",
	"audio/mp4":               ".m4a",
	"image/jpeg":              ".jpg",
	"image/png":               ".png",
	"image/gif":               ".gif",
	"application/pdf":         ".pdf",
	"application/zip":         ".zip",
	"application/x-tgsticker": ".tgs",
}

// extensionFor 按 MIME 猜测扩展名，未知类型退回 .bin。
func extensionFor(mimeType string) string {
	if ext, ok := mimeExtensions[strings.ToLower(strings.TrimSpace(mimeType))]; ok {
		return ext
	}
	return ".bin"
}
