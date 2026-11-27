package mtproto

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/ma6254/tg_down/internal/link"
)

// 评论列表的默认与最大条数。
const (
	DefaultCommentsLimit = 20
	MaxCommentsLimit     = 50
)

// CommentSection 是帖子评论区的摘要，供链接预览直接展示入口。
type CommentSection struct {
	// Available 表示帖子开启了评论区（即使暂时没有评论）。
	Available bool
	Count     int
}

// CommentsOptions 是读取评论区时的分页参数。
type CommentsOptions struct {
	// Limit 是一次返回的评论数；0 表示默认 20，超过 50 会被截断。
	Limit int
	// OffsetID 是分页游标：只返回比该消息更旧的评论；0 表示从最新开始。
	OffsetID int
	// All 为 true 时在同一个连接里连续翻页；下载评论区用它一次取回多页。
	All bool
	// MaxPages 限制 All 模式最多读取的页数；0 表示默认 10 页，最大 20 页。
	MaxPages int
}

// CommentReply 是评论引用的回复目标。
type CommentReply struct {
	MessageID int
	// Quote 是被引用的一段文字（可空）。
	Quote string
}

// Comment 是评论区里的一条评论。
type Comment struct {
	ID     int
	Date   time.Time
	Sender *Sender
	Text   string
	Links  []MessageLink
	// Entities 与正文解析保持一致，前端用同一套 MessageText 渲染。
	Entities []MessageEntity
	Media    *Media
	// MediaError 记录媒体解析失败的原因；评论本身仍然返回。
	MediaError string
	ReplyTo    *CommentReply
}

// CommentsResult 是一个帖子评论区的读取结果。
type CommentsResult struct {
	Target link.Target
	// Available 表示帖子确实开启了评论区（即使暂时没有评论）。
	Available bool
	// Reason 在 Available=false 时说明原因，例如「没有开启评论」。
	Reason string

	// Chat 是评论区所在的关联讨论组。
	Chat       *Peer
	ChatAvatar *PeerPhoto
	// PostChat 是原帖所在的频道。
	PostChat       *Peer
	PostChatAvatar *PeerPhoto

	PostID   int
	ThreadID int
	Total    int
	Comments []Comment
	// NextOffsetID 用于加载更早的评论；0 表示没有更多。
	NextOffsetID int
}

// CommentThread 是帖子在讨论组里的定位结果。
//
// 它包含「帖子 → 关联讨论组 → 线程起点」的完整映射，缓存后可以跳过
// 读帖子、查讨论组、定位线程三个 RPC。
type CommentThread struct {
	Post          Peer
	Chat          Peer
	ThreadID      int
	PostPhotoID   int64
	LinkedPhotoID int64
}

// ResolveComments 读取频道帖子的评论区。
//
// 只支持广播频道：先读帖子上的 replies 信息，再通过频道的关联讨论组
// 找到讨论消息（相册帖会有多条副本，取 ID 最小的那条作为线程起点），
// 最后调用 messages.getReplies 分页读取评论。
//
// hint 是调用方缓存下来的线程定位结果；非空时跳过前三步，直接读评论页。
func (s Session) ResolveComments(ctx context.Context, target link.Target, opts CommentsOptions,
	hint *CommentThread) (*CommentsResult, error) {
	if target.MessageID <= 0 {
		return nil, errors.New("链接里没有消息 ID")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultCommentsLimit
	}
	if limit > MaxCommentsLimit {
		limit = MaxCommentsLimit
	}
	offsetID := opts.OffsetID
	if offsetID < 0 {
		offsetID = 0
	}

	result := CommentsResult{
		Target:   target,
		PostID:   target.MessageID,
		Comments: []Comment{},
	}

	// 线程定位：hint 非空时直接用缓存的会话与线程，省掉读帖子、查讨论组、
	// 定位线程三个 RPC；否则走完整的定位流程。
	var (
		peer     Peer
		linked   Peer
		threadID int
		// postReplies 是帖子上的评论总数；命中线程缓存时没有这个值，退回
		// getReplies 响应里的计数。
		postReplies int
	)
	if hint != nil && hint.ThreadID > 0 {
		peer, linked, threadID = hint.Post, hint.Chat, hint.ThreadID
		result.PostChat = &peer
		result.Chat = &linked
		if hint.PostPhotoID != 0 {
			result.PostChatAvatar = &PeerPhoto{Peer: peer, PhotoID: hint.PostPhotoID}
		}
		if hint.LinkedPhotoID != 0 {
			result.ChatAvatar = &PeerPhoto{Peer: linked, PhotoID: hint.LinkedPhotoID}
		}
	}

	err := s.runWithClient(ctx, func(ctx context.Context, client *telegram.Client, self *tg.User) error {
		maxSize := accountMaxSize(self)
		api := client.API()

		if threadID == 0 {
			resolver := newPeerResolver(api)
			var err error
			peer, err = resolver.resolve(ctx, target)
			if err != nil {
				return err
			}
			result.PostChat = &peer
			if !peer.Broadcast {
				result.Reason = "只有频道帖子支持评论区"
				return nil
			}

			messages, _, postPhotoID, err := fetchMessages(ctx, api, peer, []int{target.MessageID})
			if err != nil {
				return err
			}
			if postPhotoID != 0 {
				result.PostChatAvatar = &PeerPhoto{Peer: peer, PhotoID: postPhotoID}
			}
			post, ok := messages[target.MessageID]
			if !ok {
				return &MessageGoneError{MessageID: target.MessageID}
			}
			replies, ok := post.GetReplies()
			if !ok || !replies.Comments {
				result.Reason = "该帖子没有开启评论"
				return nil
			}
			postReplies = replies.Replies

			var linkedPhotoID int64
			linked, linkedPhotoID, err = resolver.linkedChatInfo(ctx, peer)
			if err != nil {
				return err
			}
			result.Chat = &linked
			if linkedPhotoID != 0 {
				result.ChatAvatar = &PeerPhoto{Peer: linked, PhotoID: linkedPhotoID}
			}

			postPeer, err := peer.inputPeer()
			if err != nil {
				return err
			}
			discussion, err := api.MessagesGetDiscussionMessage(ctx, &tg.MessagesGetDiscussionMessageRequest{
				Peer:  postPeer,
				MsgID: target.MessageID,
			})
			if err != nil {
				return fmt.Errorf("查询评论线程: %w", friendlyError(err))
			}
			threadID = threadIDFromMessages(discussion.Messages)
			if threadID == 0 {
				result.Reason = "讨论组里没有找到这个帖子"
				return nil
			}
		}
		result.ThreadID = threadID

		linkedPeer, err := linked.inputPeer()
		if err != nil {
			return err
		}

		maxPages := 1
		if opts.All {
			maxPages = opts.MaxPages
			if maxPages <= 0 {
				maxPages = 10
			}
			if maxPages > 20 {
				maxPages = 20
			}
		}

		pageOffset := offsetID
		for page := 0; page < maxPages; page++ {
			replyRes, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
				Peer:     linkedPeer,
				MsgID:    threadID,
				OffsetID: pageOffset,
				Limit:    limit,
			})
			if err != nil {
				return fmt.Errorf("读取评论: %w", friendlyError(err))
			}

			commentUsers := usersOf(replyRes)
			commentChannels := channelMapOf(chatsOf(replyRes))
			raw := messagesOf(replyRes)
			for _, item := range raw {
				msg, ok := item.(*tg.Message)
				if !ok {
					continue
				}
				if msg.ID == threadID {
					continue
				}
				result.Comments = append(result.Comments,
					commentFromMessage(msg, threadID, commentUsers, commentChannels, linked, maxSize))
			}

			result.Available = true
			if result.Total <= 0 {
				result.Total = postReplies
				if result.Total <= 0 {
					result.Total = commentCountOf(replyRes)
				}
			}
			result.NextOffsetID = nextCommentsOffset(
				len(raw), limit, result.Total, len(result.Comments), lastMessageID(replyRes))
			if !opts.All || result.NextOffsetID == 0 {
				break
			}
			pageOffset = result.NextOffsetID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// commentFromMessage 把一条讨论组消息映射成评论；媒体解析失败时保留原因。
//
// threadID 用来过滤「伪回复」：讨论组里每条消息的 reply_to 都指向线程起点
// （帖子在讨论组的副本），那不是回复某条评论，界面上不该显示。
func commentFromMessage(
	msg *tg.Message,
	threadID int,
	users map[int64]*tg.User,
	channels map[int64]*tg.Channel,
	linked Peer,
	maxSize int64,
) Comment {
	text, entities := trimMessage(msg.Message, messageEntities(msg))
	comment := Comment{
		ID:       msg.ID,
		Date:     unixTime(msg.Date),
		Sender:   commentSenderOf(msg, users, channels),
		Text:     text,
		Links:    messageLinks(msg),
		Entities: entities,
		ReplyTo:  commentReplyOf(msg, threadID),
	}

	media, err := mediaFromMessage(msg)
	if err != nil {
		if !errors.Is(err, ErrNoMedia) {
			comment.MediaError = err.Error()
		}
		return comment
	}
	media.MaxSize = maxSize
	media.Downloadable = media.Size <= maxSize
	// 评论里的媒体用评论消息自己的链接，而不是频道帖子链接。
	media.Link = linked.MessageURL(msg.ID)
	comment.Media = media
	return comment
}

// commentSenderOf 优先解析用户发送者，其次解析「以频道身份」发送的评论。
func commentSenderOf(msg *tg.Message, users map[int64]*tg.User, channels map[int64]*tg.Channel) *Sender {
	if sender := senderOf(msg, users); sender != nil {
		return sender
	}
	from, ok := msg.FromID.(*tg.PeerChannel)
	if !ok || from == nil {
		return nil
	}
	channel, ok := channels[from.ChannelID]
	if !ok || channel == nil {
		return nil
	}
	peer := peerFromChannel(channel)
	sender := &Sender{Peer: peer, Name: peer.Title, Username: peer.Username}
	if photoID := chatPhotoID(channel.Photo); photoID != 0 {
		sender.Avatar = &PeerPhoto{Peer: peer, PhotoID: photoID}
	}
	return sender
}

// commentReplyOf 取出评论的回复目标与引用文字。
//
// 讨论组里每条消息都带 reply_to，通常指向线程起点（帖子在讨论组的副本），
// 这属于「评论挂在帖子下面」而不是「回复某条评论」；只有指向其它评论时
// 才返回，避免每条评论下面都挂一行「回复 #线程ID」。
func commentReplyOf(msg *tg.Message, threadID int) *CommentReply {
	header, ok := msg.GetReplyTo()
	if !ok {
		return nil
	}
	reply, ok := header.(*tg.MessageReplyHeader)
	if !ok {
		return nil
	}
	messageID, ok := reply.GetReplyToMsgID()
	if !ok || messageID == 0 {
		return nil
	}
	if messageID == threadID {
		return nil
	}
	quote, _ := reply.GetQuoteText()
	return &CommentReply{MessageID: messageID, Quote: quote}
}

// threadIDFromMessages 找到讨论消息里的线程起点。
//
// 相册帖在讨论组里会有多条复制消息，接口返回顺序是新的在前；
// 线程起点是最早、ID 最小的那条。
func threadIDFromMessages(messages []tg.MessageClass) int {
	threadID := 0
	for _, item := range messages {
		msg, ok := item.(*tg.Message)
		if !ok || msg.ID <= 0 {
			continue
		}
		if threadID == 0 || msg.ID < threadID {
			threadID = msg.ID
		}
	}
	return threadID
}

// commentCountOf 从 getReplies 的响应里取评论总数。
func commentCountOf(res tg.MessagesMessagesClass) int {
	switch v := res.(type) {
	case *tg.MessagesChannelMessages:
		return v.Count
	case *tg.MessagesMessagesSlice:
		return v.Count
	case *tg.MessagesMessages:
		return len(v.Messages)
	default:
		return 0
	}
}

// nextCommentsOffset 计算下一页游标。
//
// 只有「本页取满」且总数确实多于已返回的评论时才继续翻页；
// 否则最后一页会白白多一次请求（例如 total=1、limit=1 时）。
func nextCommentsOffset(rawCount, limit, total, commentCount, lastID int) int {
	if rawCount < limit || lastID == 0 {
		return 0
	}
	if total > 0 && total <= commentCount {
		return 0
	}
	return lastID
}

// lastMessageID 取响应里最旧一条消息的 ID，作为下一页的 offset_id。
func lastMessageID(res tg.MessagesMessagesClass) int {
	items := messagesOf(res)
	for i := len(items) - 1; i >= 0; i-- {
		switch m := items[i].(type) {
		case *tg.Message:
			if m.ID > 0 {
				return m.ID
			}
		case *tg.MessageService:
			if m.ID > 0 {
				return m.ID
			}
		}
	}
	return 0
}

// channelMapOf 把响应里的频道建成 ID → 频道 的索引。
func channelMapOf(chats []tg.ChatClass) map[int64]*tg.Channel {
	out := make(map[int64]*tg.Channel, len(chats))
	for _, item := range chats {
		if channel, ok := item.(*tg.Channel); ok {
			out[channel.ID] = channel
		}
	}
	return out
}
