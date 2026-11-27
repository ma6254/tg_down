package serve

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ma6254/tg_down/internal/config"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/msgcache"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// commentsTimeout 限制一次评论区读取的联网时间。
const commentsTimeout = 45 * time.Second

// getComments 读取频道帖子的评论区。
//
// 「帖子 → 讨论组线程」会缓存 30 天：命中时只读一页评论，不再重复解析会话、
// 读帖子、查讨论组。评论内容本身不缓存，所以不会看到过期的评论列表。
func getComments(ctx context.Context, query server.CommentsQuery) (server.CommentsResponse, error) {
	if err := ctx.Err(); err != nil {
		return server.CommentsResponse{}, err
	}

	target, err := link.Parse(strings.TrimSpace(query.Link))
	if err != nil {
		return server.CommentsResponse{}, fmt.Errorf("%w: %v", server.ErrCommentsBadRequest, err)
	}
	if target.MessageID <= 0 {
		return server.CommentsResponse{}, fmt.Errorf("%w: 链接里没有消息 ID", server.ErrCommentsBadRequest)
	}

	db, err := utils.OpenDatabase()
	if err != nil {
		return server.CommentsResponse{}, fmt.Errorf("打开数据库: %w", err)
	}
	defer db.Close()

	session, accountID, err := previewSession(db)
	if err != nil {
		return server.CommentsResponse{}, err
	}

	cache := msgcache.New(db)
	// 与消息缓存共用开关：关掉缓存时连线程定位也不读不写。
	enabled, err := config.NewStore(db).Bool(config.KeyMessageCacheEnabled)
	if err != nil {
		log.Printf("读取消息缓存开关失败（按开启处理）: %v", err)
		enabled = true
	}

	var hint *mtproto.CommentThread
	if enabled {
		hint = cache.LoadThread(accountID, target)
	}

	callCtx, cancel := context.WithTimeout(ctx, commentsTimeout)
	defer cancel()

	started := time.Now()
	result, err := session.ResolveComments(callCtx, target, mtproto.CommentsOptions{
		Limit:    query.Limit,
		OffsetID: query.OffsetID,
	}, hint)
	elapsed := time.Since(started)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return server.CommentsResponse{}, err
		}
		return server.CommentsResponse{}, fmt.Errorf("%w: %v", server.ErrCommentsFailed, err)
	}
	logComments(target, hint != nil, elapsed)

	if enabled {
		saveThread(cache, accountID, target, result)
	}
	return server.NewCommentsResponse(*result, session), nil
}

// logComments 记录评论区耗时，只在明显慢时输出。
//
// 用来区分「服务端读评论慢」和「浏览器在加载评论里的图片」：
// 这里只统计到响应生成完成为止，图片是之后才由前端逐个请求的。
func logComments(target link.Target, cacheHit bool, elapsed time.Duration) {
	if elapsed < 500*time.Millisecond {
		return
	}
	hit := "未命中"
	if cacheHit {
		hit = "命中"
	}
	log.Printf("评论区 %s 耗时 %s（线程缓存%s）",
		target.Raw, elapsed.Round(time.Millisecond), hit)
}

// saveThread 把定位结果写回缓存；失败只记日志，不影响响应。
func saveThread(cache *msgcache.Cache, accountID int64, target link.Target, result *mtproto.CommentsResult) {
	if result.ThreadID <= 0 || result.PostChat == nil || result.Chat == nil {
		return
	}
	thread := mtproto.CommentThread{
		Post:     *result.PostChat,
		Chat:     *result.Chat,
		ThreadID: result.ThreadID,
	}
	if result.PostChatAvatar != nil {
		thread.PostPhotoID = result.PostChatAvatar.PhotoID
	}
	if result.ChatAvatar != nil {
		thread.LinkedPhotoID = result.ChatAvatar.PhotoID
	}
	if err := cache.SaveThread(accountID, target, thread); err != nil {
		log.Printf("写入评论线程缓存失败: %v", err)
	}
}
