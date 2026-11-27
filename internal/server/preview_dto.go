package server

import (
	"github.com/ma6254/tg_down/internal/avatar"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/thumbcache"
)

// NewPreviewErrorItem 为无法解析的链接生成一条只带错误的结果。
//
// 单条链接失败不影响其余链接，前端逐条展示原因。
func NewPreviewErrorItem(raw string, err error) PreviewItem {
	return PreviewItem{Link: raw, Error: err.Error()}
}

// NewPreviewItems 把 mtproto 的解析结果转换成接口结构。
//
// 封面与头像在这里登记为一次性 token 地址；expanded 标记这段结果是否来自
// 正文链接展开，前端据此分组展示。
func NewPreviewItems(results []mtproto.LinkResult, expanded bool, session mtproto.Session) []PreviewItem {
	out := make([]PreviewItem, 0, len(results))
	for _, result := range results {
		item := PreviewItem{
			Link:     result.Target.Raw,
			Expanded: expanded,
			Cached:   result.Cached,
			Stale:    result.Stale,
			Gone:     result.Gone,
		}
		if !result.CachedAt.IsZero() {
			item.CachedAt = result.CachedAt.Unix()
		}
		if result.Err != nil {
			item.Error = result.Err.Error()
			out = append(out, item)
			continue
		}

		item.MessageID = result.MessageID
		item.Text = result.Text
		chat := newPreviewChat(result.Chat, result.ChatAvatar, session)
		item.Chat = &chat
		if result.Sender != nil {
			sender := newPreviewSender(result.Sender, session)
			item.Sender = &sender
		}
		item.Links = newPreviewLinks(result.Links)
		item.Entities = newPreviewEntities(result.Entities)
		if result.CommentSection != nil {
			item.CommentSection = &PreviewCommentSection{
				Available: result.CommentSection.Available,
				Count:     result.CommentSection.Count,
			}
		}
		if media := result.Media; media != nil {
			item.Media = newPreviewMedia(media, session)
		}
		if len(result.Medias) > 0 {
			item.Medias = make([]PreviewMedia, 0, len(result.Medias))
			for _, media := range result.Medias {
				item.Medias = append(item.Medias, *newPreviewMedia(media, session))
			}
		}
		out = append(out, item)
	}
	return out
}

// newPreviewChat 把会话摘要映射成接口结构，并按需登记头像。
func newPreviewChat(chat mtproto.Peer, avatar *mtproto.PeerPhoto, session mtproto.Session) PreviewChat {
	preview := PreviewChat{
		Kind:      string(chat.Kind),
		ID:        chat.ID,
		Title:     chat.Title,
		Username:  chat.Username,
		Broadcast: chat.Broadcast,
		Self:      chat.Self,
	}
	if avatar != nil {
		preview.AvatarURL = newPeerAvatarURL(session, avatar)
	}
	return preview
}

// newPreviewSender 把发送者映射成接口结构，并按需登记头像。
func newPreviewSender(sender *mtproto.Sender, session mtproto.Session) PreviewSender {
	preview := PreviewSender{
		ID:       sender.Peer.ID,
		Name:     sender.Name,
		Username: sender.Username,
	}
	if sender.Avatar != nil {
		preview.AvatarURL = newPeerAvatarURL(session, sender.Avatar)
	}
	return preview
}

// newPreviewLinks 复制消息正文里的文字链接。
func newPreviewLinks(links []mtproto.MessageLink) []PreviewLink {
	if len(links) == 0 {
		return nil
	}
	out := make([]PreviewLink, 0, len(links))
	for _, link := range links {
		out = append(out, PreviewLink{Text: link.Text, URL: link.URL})
	}
	return out
}

// newPreviewEntities 复制正文富文本标注。
func newPreviewEntities(entities []mtproto.MessageEntity) []PreviewEntity {
	if len(entities) == 0 {
		return nil
	}
	out := make([]PreviewEntity, 0, len(entities))
	for _, entity := range entities {
		out = append(out, PreviewEntity{
			Type:       entity.Type,
			Offset:     entity.Offset,
			Length:     entity.Length,
			URL:        entity.URL,
			UserID:     entity.UserID,
			Language:   entity.Language,
			DocumentID: entity.DocumentID,
			Collapsed:  entity.Collapsed,
			Date:       entity.Date,
			DateFormat: entity.DateFormat,
		})
	}
	return out
}

// newPreviewMedia 把单条媒体转换成接口结构，并登记封面/大图的缩略图 token。
func newPreviewMedia(media *mtproto.Media, session mtproto.Session) *PreviewMedia {
	thumbnailURL := ""
	thumbKey := ""
	if media.Thumb != nil {
		thumbnailURL = "/api/v1/media/thumbnail/" + RegisterThumbnail(session, *media.Thumb)
		// 磁盘缓存键与 token 无关，历史卡片可以长期引用。
		thumbKey = thumbcache.Key(*media.Thumb)
	}
	previewURL := ""
	if media.Preview != nil {
		previewURL = "/api/v1/media/thumbnail/" + RegisterThumbnail(session, *media.Preview)
	}
	return &PreviewMedia{
		Kind:         string(media.Kind),
		FileName:     media.FileName,
		Size:         media.Size,
		MessageID:    media.MessageID,
		Link:         media.Link,
		MIME:         media.MIME,
		Duration:     media.Duration,
		Width:        media.Width,
		Height:       media.Height,
		GroupedID:    media.GroupedID,
		Downloadable: media.Downloadable,
		MaxSize:      media.MaxSize,
		ThumbnailURL: thumbnailURL,
		PreviewURL:   previewURL,
		ThumbKey:     thumbKey,
	}
}

// newPeerAvatarURL 登记一张群聊/频道/发送者头像，返回可直接放进响应的地址。
//
// 缓存路径取不到时返回空串，前端回退到名称首字母。
func newPeerAvatarURL(session mtproto.Session, photo *mtproto.PeerPhoto) string {
	path, err := avatar.PeerPath(photo.Peer, photo.PhotoID)
	if err != nil {
		return ""
	}
	token := RegisterPeerPhoto(session, photo.Peer, photo.PhotoID, path)
	return "/api/v1/media/thumbnail/" + token
}

// NewPreviewResponse 统计成功、失败、无媒体与展开数量。
func NewPreviewResponse(items []PreviewItem, skippedLinks int) PreviewResponse {
	summary := PreviewSummary{
		Total:        len(items),
		SkippedLinks: skippedLinks,
	}
	for _, item := range items {
		if item.Error != "" {
			summary.Failed++
			continue
		}
		summary.OK++
		if item.Media == nil {
			summary.NoMedia++
		}
		if item.Expanded {
			summary.Expanded++
		}
	}
	return PreviewResponse{Items: items, Summary: summary}
}
