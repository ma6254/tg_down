package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/server"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
	"github.com/ma6254/tg_down/internal/utils"
)

// commentsTimeout 限制一次评论区读取的联网时间。
const commentsTimeout = 45 * time.Second

// getComments 读取频道帖子的评论区。
//
// 只联网读取，不写数据库、不创建下载任务。回复组、分页与评论映射
// 在 internal/tgclient/mtproto 完成，本函数只负责依赖装配与错误归一。
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

	session, err := previewSession(db)
	if err != nil {
		return server.CommentsResponse{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, commentsTimeout)
	defer cancel()

	result, err := session.ResolveComments(callCtx, target, mtproto.CommentsOptions{
		Limit:    query.Limit,
		OffsetID: query.OffsetID,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return server.CommentsResponse{}, err
		}
		return server.CommentsResponse{}, fmt.Errorf("%w: %v", server.ErrCommentsFailed, err)
	}
	return server.NewCommentsResponse(*result, session), nil
}
