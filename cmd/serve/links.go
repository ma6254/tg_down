package serve

import (
	"context"
	"errors"
	"fmt"
	"time"

	accountsvc "github.com/ma6254/tg_down/internal/account"
	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

const (
	// maxPreviewExpand 是接口一次最多展开的正文链接数，与 CLI 保持一致。
	maxPreviewExpand = 200
	// 解析超时按链接数放宽：建立连接 + 每条一次批量取消息。
	previewBaseTimeout = 60 * time.Second
	previewPerLink     = 5 * time.Second
	previewMaxTimeout  = 5 * time.Minute
)

// previewLinks 解析 Telegram 消息链接，等价于 CLI 的：
//
//	tg_down download -u <链接> [--expand-text-links]
//
// 只联网读取消息，不写数据库、不创建下载任务。
func previewLinks(ctx context.Context, req server.PreviewRequest) (server.PreviewResponse, error) {
	if err := ctx.Err(); err != nil {
		return server.PreviewResponse{}, err
	}

	// 先做纯字符串解析：全部非法时直接返回逐条错误，不必连 Telegram。
	parsed := link.ParseAll(req.Links)
	items := make([]server.PreviewItem, 0, len(parsed))
	targets := make([]link.Target, 0, len(parsed))
	for _, result := range parsed {
		if result.Err != nil {
			items = append(items, server.PreviewItem{
				Link:  result.Raw,
				Error: result.Err.Error(),
			})
			continue
		}
		targets = append(targets, result.Target)
	}
	if len(targets) == 0 {
		return buildPreviewResponse(items, 0), nil
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.PreviewResponse{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	service := accountsvc.New(db)
	id, ok, err := service.CurrentAccountID()
	if err != nil {
		return server.PreviewResponse{}, err
	}
	if !ok {
		return server.PreviewResponse{}, fmt.Errorf("%w：先执行 account switch <账号ID>", server.ErrNoCurrentAccount)
	}

	account, err := service.Get(id)
	if err != nil {
		return server.PreviewResponse{}, fmt.Errorf("读取账号 #%d: %w", id, err)
	}
	if account.Kind != database.AccountKindMTProto {
		return server.PreviewResponse{}, fmt.Errorf("%w：账号 #%d 是 %s，按链接解析暂只支持 mtproto",
			server.ErrNoCurrentAccount, id, account.Kind)
	}
	if !utils.FileExists(account.SessionPath) {
		return server.PreviewResponse{}, fmt.Errorf("%w：账号 #%d 的 session 文件不存在，请重新登录",
			server.ErrAccountNoSession, account.ID)
	}

	proxy, err := config.NewStore(db).Proxy()
	if err != nil {
		return server.PreviewResponse{}, fmt.Errorf("读取代理配置: %w", err)
	}
	app, err := service.AppOrDefault()
	if err != nil {
		return server.PreviewResponse{}, err
	}
	session := mtproto.Session{App: app, SessionPath: account.SessionPath, Proxy: proxy}

	results, err := previewResolve(ctx, session, targets)
	if err != nil {
		return server.PreviewResponse{}, err
	}
	items = append(items, buildPreviewItems(results, false, session)...)

	var skippedLinks int
	var truncated bool
	if req.ExpandTextLinks {
		plan := mtproto.PlanLinkExpansion(results, targets, maxPreviewExpand)
		skippedLinks, truncated = plan.Skipped, plan.Truncated

		if len(plan.Targets) > 0 {
			expanded, err := previewResolve(ctx, session, plan.Targets)
			if err != nil {
				return server.PreviewResponse{}, err
			}
			items = append(items, buildPreviewItems(expanded, true, session)...)
		}
	}

	resp := buildPreviewResponse(items, skippedLinks)
	resp.Summary.Truncated = truncated
	return resp, nil
}

// previewResolve 按链接数计算超时后调用一次批量解析。
func previewResolve(ctx context.Context, session mtproto.Session, targets []link.Target) ([]mtproto.LinkResult, error) {
	timeout := previewBaseTimeout + time.Duration(len(targets))*previewPerLink
	if timeout > previewMaxTimeout {
		timeout = previewMaxTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := session.ResolveLinks(callCtx, targets)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", server.ErrPreviewFailed, err)
	}
	return results, nil
}

// buildPreviewItems 把 mtproto 结果转换成接口结构。
func buildPreviewItems(results []mtproto.LinkResult, expanded bool, session mtproto.Session) []server.PreviewItem {
	out := make([]server.PreviewItem, 0, len(results))
	for _, result := range results {
		item := server.PreviewItem{
			Link:     result.Target.Raw,
			Expanded: expanded,
		}
		if result.Err != nil {
			item.Error = result.Err.Error()
			out = append(out, item)
			continue
		}

		item.MessageID = result.MessageID
		item.Text = result.Text
		item.Chat = &server.PreviewChat{
			Kind:      string(result.Chat.Kind),
			ID:        result.Chat.ID,
			Title:     result.Chat.Title,
			Username:  result.Chat.Username,
			Broadcast: result.Chat.Broadcast,
			Self:      result.Chat.Self,
		}
		if result.ChatAvatar != nil {
			item.Chat.AvatarURL = peerAvatarURL(session, result.ChatAvatar)
		}
		if result.Sender != nil {
			sender := &server.PreviewSender{
				ID:       result.Sender.Peer.ID,
				Name:     result.Sender.Name,
				Username: result.Sender.Username,
			}
			if result.Sender.Avatar != nil {
				sender.AvatarURL = peerAvatarURL(session, result.Sender.Avatar)
			}
			item.Sender = sender
		}
		for _, messageLink := range result.Links {
			item.Links = append(item.Links, server.PreviewLink{
				Text: messageLink.Text,
				URL:  messageLink.URL,
			})
		}
		for _, entity := range result.Entities {
			item.Entities = append(item.Entities, server.PreviewEntity{
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
		if media := result.Media; media != nil {
			item.Media = buildPreviewMedia(media, session)
		}
		if len(result.Medias) > 0 {
			item.Medias = make([]server.PreviewMedia, 0, len(result.Medias))
			for _, media := range result.Medias {
				item.Medias = append(item.Medias, *buildPreviewMedia(media, session))
			}
		}
		out = append(out, item)
	}
	return out
}

// buildPreviewMedia 把单条媒体转换成接口结构，并登记封面/大图的缩略图 token。
func buildPreviewMedia(media *mtproto.Media, session mtproto.Session) *server.PreviewMedia {
	thumbnailURL := ""
	if media.Thumb != nil {
		thumbnailURL = "/api/v1/media/thumbnail/" + server.RegisterThumbnail(session, *media.Thumb)
	}
	previewURL := ""
	if media.Preview != nil {
		previewURL = "/api/v1/media/thumbnail/" + server.RegisterThumbnail(session, *media.Preview)
	}
	return &server.PreviewMedia{
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
	}
}

// buildPreviewResponse 统计成功、失败、无媒体与展开数量。
func buildPreviewResponse(items []server.PreviewItem, skippedLinks int) server.PreviewResponse {
	summary := server.PreviewSummary{
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
	return server.PreviewResponse{Items: items, Summary: summary}
}
