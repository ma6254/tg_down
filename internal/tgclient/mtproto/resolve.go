package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"github.com/ma6254/tg_down/internal/link"
)

// PeerKind 是会话类型。
type PeerKind string

const (
	PeerKindUser    PeerKind = "user"
	PeerKindGroup   PeerKind = "group"
	PeerKindChannel PeerKind = "channel"
)

// 会话列表分页参数：每页 100 条，最多翻 5 页。
const (
	dialogPageSize = 100
	maxDialogPages = 5
)

// Peer 是一个会话的定位信息；普通群的 AccessHash 为 0。
type Peer struct {
	Kind       PeerKind
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	Broadcast  bool // 广播频道（评论链接只对广播频道有意义）
	Self       bool // 用户会话是否就是当前账号（收藏夹）
}

// inputPeer 返回后续请求可复用的 gotd peer。
func (p Peer) inputPeer() (tg.InputPeerClass, error) {
	switch p.Kind {
	case PeerKindChannel:
		return &tg.InputPeerChannel{ChannelID: p.ID, AccessHash: p.AccessHash}, nil
	case PeerKindGroup:
		return &tg.InputPeerChat{ChatID: p.ID}, nil
	case PeerKindUser:
		return &tg.InputPeerUser{UserID: p.ID, AccessHash: p.AccessHash}, nil
	}
	return nil, fmt.Errorf("未知会话类型 %q", p.Kind)
}

// inputChannel 返回频道入参，只有频道/超级群可用。
func (p Peer) inputChannel() (tg.InputChannelClass, error) {
	if p.Kind != PeerKindChannel {
		return nil, fmt.Errorf("会话 %s 不是频道", p.Title)
	}
	return &tg.InputChannel{ChannelID: p.ID, AccessHash: p.AccessHash}, nil
}

// PeerPhoto 是会话或用户头像的定位信息（用 inputPeerPhotoFileLocation 下载）。
type PeerPhoto struct {
	Peer    Peer
	PhotoID int64
}

// Sender 是群聊消息的发送者（不含广播频道，频道帖子没有个人发送者）。
type Sender struct {
	Peer     Peer
	Name     string
	Username string
	Self     bool
	Avatar   *PeerPhoto
}

// LinkResult 是一条链接的查询结果；Err 非空时其它字段无意义。
//
// Text 是消息正文（媒体消息则是说明文字）。纯文本消息没有 Media，
// 但 Text 仍然有值，下载流程可以据此提示「没有可下载的媒体」。
// Links 是正文里内嵌的链接（text_link / url 实体），按出现顺序去重。
// Entities 是正文的完整富文本标注，偏移已与裁剪后的 Text 对齐。
// Medias 是相册的全部媒体（按消息 ID 升序）；非相册消息为空，只用 Media。
type LinkResult struct {
	Target     link.Target
	Chat       Peer
	ChatAvatar *PeerPhoto
	Sender     *Sender
	MessageID  int
	Text       string
	Links      []MessageLink
	Entities   []MessageEntity
	Media      *Media
	Medias     []*Media
	// CommentSection 是频道帖子的评论区摘要；普通消息/评论链接为空。
	CommentSection *CommentSection
	Err            error
}

// MessageURL 返回一条消息的链接；私聊等拼不出链接的场景返回空串。
func (p Peer) MessageURL(messageID int) string {
	if messageID <= 0 {
		return ""
	}
	if p.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", p.Username, messageID)
	}
	if p.Kind == PeerKindChannel {
		return fmt.Sprintf("https://t.me/c/%d/%d", p.ID, messageID)
	}
	return ""
}

// ResolveLinks 解析链接指向的会话与消息，并提取媒体信息。
//
// 多条链接共用一次连接和会话缓存；单条失败写进对应结果的 Err，不影响其它链接。
// 返回 error 表示连接或登录状态等整体性失败。
func (s Session) ResolveLinks(ctx context.Context, targets []link.Target) ([]LinkResult, error) {
	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return nil, errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return nil, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("未找到 session 文件 %s，请先执行 account login", opts.SessionPath)
		}
		return nil, fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}

	results := make([]LinkResult, len(targets))
	for i := range targets {
		results[i] = LinkResult{Target: targets[i]}
	}

	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return friendlyError(fmt.Errorf("查询登录状态: %w", err))
		}
		if !status.Authorized {
			return errors.New("session 已失效，请重新执行 account login")
		}

		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("读取账号信息: %w", err)
		}
		maxSize := MaxFileSize
		if self.Premium {
			maxSize = MaxFileSizePremium
		}

		resolver := newPeerResolver(client.API())

		// 先解析每条链接的会话与消息 ID，再按会话分组批量取消息：
		// 几十条评论只发一次 channels.getMessages，避免逐条 getHistory 触发 FLOOD_WAIT。
		peers := make([]Peer, len(results))
		messageIDs := make([]int, len(results))
		groups := make(map[string][]int)
		for i := range results {
			peer, messageID, err := resolver.resolveTarget(ctx, results[i].Target)
			if err != nil {
				results[i].Err = err
				continue
			}
			peers[i], messageIDs[i] = peer, messageID
			key := peerKey(peer.Kind, peer.ID)
			groups[key] = append(groups[key], i)
		}

		for _, indexes := range groups {
			peer := peers[indexes[0]]
			ids := make([]int, 0, len(indexes))
			for _, i := range indexes {
				ids = append(ids, messageIDs[i])
			}

			messages, users, chatPhotoID, err := fetchMessages(ctx, client.API(), peer, ids)
			if err != nil {
				for _, i := range indexes {
					results[i].Err = err
				}
				continue
			}

			for _, i := range indexes {
				msg, ok := messages[messageIDs[i]]
				if !ok {
					results[i].Err = fmt.Errorf("消息 %d 不存在或已被删除", messageIDs[i])
					continue
				}

				results[i].Chat = peers[i]
				if chatPhotoID != 0 {
					results[i].ChatAvatar = &PeerPhoto{Peer: peers[i], PhotoID: chatPhotoID}
				}
				if groupLike(peers[i]) {
					results[i].Sender = senderOf(msg, users)
				}
				results[i].MessageID = messageIDs[i]
				if !targets[i].IsComment() {
					if replies, ok := msg.GetReplies(); ok && replies.Comments {
						results[i].CommentSection = &CommentSection{
							Available: true,
							Count:     replies.Replies,
						}
					}
				}
				text, entities := trimMessage(msg.Message, messageEntities(msg))
				results[i].Text = text
				results[i].Entities = entities
				results[i].Links = messageLinks(msg)

				media, err := mediaFromMessage(msg)
				if err != nil {
					// 纯文本消息不算失败：没有文件可下，但正文仍然有展示价值。
					if errors.Is(err, ErrNoMedia) {
						continue
					}
					results[i].Err = err
					continue
				}

				media.MaxSize = maxSize
				media.Downloadable = media.Size <= maxSize
				media.Link = mediaLink(targets[i], media, peers[i])
				results[i].Media = media

				// 相册的每条媒体是独立消息：链接通常指向第一张，
				// 只取这一条会把整组图片/视频漏掉。
				if media.GroupedID != 0 {
					album, err := fetchAlbum(ctx, client.API(), peers[i], msg.ID, media.GroupedID)
					if err != nil {
						// 相册读取失败不影响已拿到的这一条。
						continue
					}
					results[i].Medias = albumMedias(peers[i], album, media.GroupedID, maxSize)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// ResolveLink 是单条链接的便捷封装。
func (s Session) ResolveLink(ctx context.Context, target link.Target) (*LinkResult, error) {
	results, err := s.ResolveLinks(ctx, []link.Target{target})
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, errors.New("解析结果数量异常")
	}
	if results[0].Err != nil {
		return nil, results[0].Err
	}
	return &results[0], nil
}

// peerResolver 在一次连接内缓存会话，避免每条链接都重复解析用户名或拉会话列表。
type peerResolver struct {
	api           *tg.Client
	byKey         map[string]Peer
	byUsername    map[string]Peer
	linked        map[string]Peer
	linkedPhotoID map[string]int64
	dialogsLoaded bool
}

func newPeerResolver(api *tg.Client) *peerResolver {
	return &peerResolver{
		api:           api,
		byKey:         make(map[string]Peer),
		byUsername:    make(map[string]Peer),
		linked:        make(map[string]Peer),
		linkedPhotoID: make(map[string]int64),
	}
}

// mediaLink 返回单条媒体可再次解析的链接。
//
// 评论链接解析到的是讨论组里的消息，MessageURL 会给出 t.me/c/<群>/<消息>；
// 账号没加入讨论组时这个链接无法解析（预览能成是因为走了频道的关联讨论组接口）。
// 评论本身继续沿用原始 ?comment= 链接，下载阶段才能重新定位到它。
func mediaLink(target link.Target, media *Media, peer Peer) string {
	if target.IsComment() && media.MessageID == target.CommentID {
		return target.Raw
	}
	return peer.MessageURL(media.MessageID)
}

// resolveTarget 把链接定位成「会话 + 消息 ID」，评论链接会换到关联讨论组。
func (r *peerResolver) resolveTarget(ctx context.Context, target link.Target) (Peer, int, error) {
	peer, err := r.resolve(ctx, target)
	if err != nil {
		return Peer{}, 0, err
	}

	messageID := target.MessageID
	if target.CommentID > 0 {
		linked, err := r.linkedChat(ctx, peer)
		if err != nil {
			return Peer{}, 0, err
		}
		peer = linked
		messageID = target.CommentID
	}
	return peer, messageID, nil
}

// resolve 按用户名或裸频道 ID 找到会话。
func (r *peerResolver) resolve(ctx context.Context, target link.Target) (Peer, error) {
	if target.Username != "" {
		return r.resolveUsername(ctx, target.Username)
	}

	if err := r.loadDialogs(ctx); err != nil {
		return Peer{}, err
	}
	peer, ok := r.byKey[peerKey(PeerKindChannel, target.ChannelID)]
	if !ok {
		return Peer{}, fmt.Errorf("当前账号的会话列表里没有频道 %d；请确认已用该账号加入该频道", target.ChannelID)
	}
	return peer, nil
}

// resolveUsername 通过公开用户名解析会话，同一条链接的重复用户只查一次。
func (r *peerResolver) resolveUsername(ctx context.Context, username string) (Peer, error) {
	if peer, ok := r.byUsername[username]; ok {
		return peer, nil
	}

	resolved, err := r.api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return Peer{}, fmt.Errorf("解析 @%s: %w", username, friendlyError(err))
	}
	peer, err := peerFromResolved(resolved)
	if err != nil {
		return Peer{}, fmt.Errorf("解析 @%s: %w", username, err)
	}

	r.byUsername[username] = peer
	r.byKey[peerKey(peer.Kind, peer.ID)] = peer
	return peer, nil
}

// linkedChat 查询广播频道的关联讨论组；讨论组 ID 与 access hash 一起缓存。
func (r *peerResolver) linkedChat(ctx context.Context, channel Peer) (Peer, error) {
	peer, _, err := r.linkedChatInfo(ctx, channel)
	return peer, err
}

// linkedChatInfo 与 linkedChat 相同，但同时返回讨论组头像的 photo ID。
func (r *peerResolver) linkedChatInfo(ctx context.Context, channel Peer) (Peer, int64, error) {
	key := peerKey(channel.Kind, channel.ID)
	if peer, ok := r.linked[key]; ok {
		return peer, r.linkedPhotoID[key], nil
	}

	input, err := channel.inputChannel()
	if err != nil {
		return Peer{}, 0, fmt.Errorf("评论链接需要广播频道：%w", err)
	}
	full, err := r.api.ChannelsGetFullChannel(ctx, input)
	if err != nil {
		return Peer{}, 0, fmt.Errorf("查询频道 %s 的讨论组: %w", channel.Title, friendlyError(err))
	}

	cf, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return Peer{}, 0, fmt.Errorf("频道 %s 没有讨论组信息", channel.Title)
	}
	linkedID, ok := cf.GetLinkedChatID()
	if !ok || linkedID == 0 {
		return Peer{}, 0, fmt.Errorf("频道 %s 没有关联讨论组，评论链接无法定位", channel.Title)
	}

	for _, chat := range full.Chats {
		peer, ok := peerFromChatClass(chat)
		if ok && peer.ID == linkedID {
			r.linked[key] = peer
			r.linkedPhotoID[key] = chatClassPhotoID(chat)
			return peer, r.linkedPhotoID[key], nil
		}
	}
	return Peer{}, 0, fmt.Errorf("讨论组 %d 不在返回结果里，请确认已用该账号加入讨论组", linkedID)
}

// chatClassPhotoID 返回频道/普通群头像的 photo ID。
func chatClassPhotoID(chat tg.ChatClass) int64 {
	switch c := chat.(type) {
	case *tg.Channel:
		return chatPhotoID(c.Photo)
	case *tg.Chat:
		return chatPhotoID(c.Photo)
	}
	return 0
}

// loadDialogs 分页拉取会话列表，用于解析 t.me/c/ 形式的私有频道链接。
//
// 私有频道不在链接里带 access hash，只能从账号自己的会话列表里找。
func (r *peerResolver) loadDialogs(ctx context.Context) error {
	if r.dialogsLoaded {
		return nil
	}

	offsetPeer := tg.InputPeerClass(&tg.InputPeerEmpty{})
	offsetID, offsetDate := 0, 0

	for page := 0; page < maxDialogPages; page++ {
		res, err := r.api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			OffsetDate: offsetDate,
			OffsetID:   offsetID,
			OffsetPeer: offsetPeer,
			Limit:      dialogPageSize,
		})
		if err != nil {
			return fmt.Errorf("读取会话列表: %w", friendlyError(err))
		}

		dialogs, chats, users, messages, ok := dialogsOf(res)
		if !ok {
			return nil
		}
		for _, chat := range chats {
			if peer, ok := peerFromChatClass(chat); ok {
				r.byKey[peerKey(peer.Kind, peer.ID)] = peer
			}
		}
		for _, user := range users {
			if u, ok := user.(*tg.User); ok {
				peer := peerFromUser(u)
				r.byKey[peerKey(peer.Kind, peer.ID)] = peer
			}
		}

		if len(dialogs) < dialogPageSize {
			r.dialogsLoaded = true
			return nil
		}

		// 用最后一条会话作为下一页的游标；拿不到游标就停止，避免死循环。
		last, ok := dialogs[len(dialogs)-1].(*tg.Dialog)
		if !ok {
			r.dialogsLoaded = true
			return nil
		}
		nextPeer, ok := inputPeerFor(last.Peer, chats, users)
		if !ok {
			r.dialogsLoaded = true
			return nil
		}
		topDate := messageDate(messages, last.TopMessage)
		if topDate == 0 {
			r.dialogsLoaded = true
			return nil
		}

		offsetPeer, offsetID, offsetDate = nextPeer, last.TopMessage, topDate
	}

	r.dialogsLoaded = true
	return nil
}

// fetchMessages 按会话批量读取消息：频道走 channels.getMessages（每次最多 100 条），
// 其它会话逐条走 getHistory（数量通常很少）。
//
// 返回的 map 只包含真正取到的消息；调用方据此判断哪些消息已删除。
// users 与 chatPhotoID 用于群聊展示发送者、群头像；取不到的字段为空/0。
func fetchMessages(ctx context.Context, api *tg.Client, peer Peer, ids []int) (map[int]*tg.Message, map[int64]*tg.User, int64, error) {
	out := make(map[int]*tg.Message, len(ids))
	users := make(map[int64]*tg.User)
	var chatPhotoID int64

	if peer.Kind != PeerKindChannel {
		for _, id := range ids {
			msg, msgUsers, photoID, err := fetchMessage(ctx, api, peer, id)
			if err != nil {
				continue
			}
			out[msg.ID] = msg
			mergeUsers(users, msgUsers)
			if photoID != 0 {
				chatPhotoID = photoID
			}
		}
		return out, users, chatPhotoID, nil
	}

	input, err := peer.inputChannel()
	if err != nil {
		return nil, nil, 0, err
	}

	const chunkSize = 100
	for start := 0; start < len(ids); start += chunkSize {
		end := start + chunkSize
		if end > len(ids) {
			end = len(ids)
		}

		batch := ids[start:end]
		inputs := make([]tg.InputMessageClass, len(batch))
		for i, id := range batch {
			inputs[i] = &tg.InputMessageID{ID: id}
		}

		res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: input,
			ID:      inputs,
		})
		if err != nil {
			return nil, nil, 0, fmt.Errorf("读取消息: %w", friendlyError(err))
		}
		for _, item := range messagesOf(res) {
			if msg, ok := item.(*tg.Message); ok {
				out[msg.ID] = msg
			}
		}
		mergeUsers(users, usersOf(res))
		if photoID := chatPhotoOf(res, peer.ID); photoID != 0 {
			chatPhotoID = photoID
		}
	}
	return out, users, chatPhotoID, nil
}

// fetchMessage 按「消息 ID + 1」读一条历史，取回的必须是目标消息本身，
// 否则说明消息已删除；这种取法对频道、普通群和私聊都适用。
//
// 同时带回响应里的用户与群头像，供群聊展示发送者信息。
func fetchMessage(ctx context.Context, api *tg.Client, peer Peer, messageID int) (*tg.Message, map[int64]*tg.User, int64, error) {
	input, err := peer.inputPeer()
	if err != nil {
		return nil, nil, 0, err
	}

	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     input,
		OffsetID: messageID + 1,
		Limit:    1,
	})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("读取消息 %d: %w", messageID, friendlyError(err))
	}

	for _, item := range messagesOf(res) {
		msg, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		if msg.ID != messageID {
			return nil, nil, 0, fmt.Errorf("消息 %d 不存在或已被删除", messageID)
		}
		return msg, usersOf(res), chatPhotoOf(res, peer.ID), nil
	}
	return nil, nil, 0, fmt.Errorf("消息 %d 不存在或已被删除", messageID)
}

// fetchAlbum 读取消息所在相册的全部消息。
//
// 相册的每条媒体是独立消息，ID 连续且 grouped_id 相同；链接通常指向第一条，
// 只按 ID 取一条会漏掉整组。这里以目标消息为基准取一段历史再按分组过滤。
func fetchAlbum(ctx context.Context, api *tg.Client, peer Peer, messageID int, groupedID int64) ([]*tg.Message, error) {
	input, err := peer.inputPeer()
	if err != nil {
		return nil, err
	}

	// 相册最多 10 条；从 messageID+11 往回取 30 条，覆盖前后相邻项。
	offset := messageID + 11
	if offset < 11 {
		offset = 11
	}
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     input,
		OffsetID: offset,
		Limit:    30,
	})
	if err != nil {
		return nil, fmt.Errorf("读取相册: %w", friendlyError(err))
	}

	out := make([]*tg.Message, 0, 10)
	for _, item := range messagesOf(res) {
		msg, ok := item.(*tg.Message)
		if !ok {
			continue
		}
		if id, ok := msg.GetGroupedID(); ok && id == groupedID {
			out = append(out, msg)
		}
	}
	return out, nil
}

// albumMedias 把同组消息转成媒体列表，按消息 ID 升序，并补齐大小上限与链接。
func albumMedias(peer Peer, messages []*tg.Message, groupedID, maxSize int64) []*Media {
	out := make([]*Media, 0, len(messages))
	for _, msg := range messages {
		if id, ok := msg.GetGroupedID(); !ok || id != groupedID {
			continue
		}
		media, err := mediaFromMessage(msg)
		if err != nil {
			continue
		}
		media.MaxSize = maxSize
		media.Downloadable = media.Size <= maxSize
		media.Link = peer.MessageURL(media.MessageID)
		out = append(out, media)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].MessageID < out[j].MessageID
	})
	return out
}

// dialogsOf 拆出会话列表响应里的各部分。
func dialogsOf(res tg.MessagesDialogsClass) ([]tg.DialogClass, []tg.ChatClass, []tg.UserClass, []tg.MessageClass, bool) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Dialogs, v.Chats, v.Users, v.Messages, true
	case *tg.MessagesDialogsSlice:
		return v.Dialogs, v.Chats, v.Users, v.Messages, true
	}
	return nil, nil, nil, nil, false
}

// messagesOf 拆出消息列表响应里的消息。
func messagesOf(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Messages
	case *tg.MessagesMessagesSlice:
		return v.Messages
	case *tg.MessagesChannelMessages:
		return v.Messages
	}
	return nil
}

// chatsOf 取出响应里的会话列表（含群头像）。
func chatsOf(res tg.MessagesMessagesClass) []tg.ChatClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Chats
	case *tg.MessagesMessagesSlice:
		return v.Chats
	case *tg.MessagesChannelMessages:
		return v.Chats
	}
	return nil
}

// usersOf 把响应里的用户（发送者）转成 ID 索引。
func usersOf(res tg.MessagesMessagesClass) map[int64]*tg.User {
	var list []tg.UserClass
	switch v := res.(type) {
	case *tg.MessagesMessages:
		list = v.Users
	case *tg.MessagesMessagesSlice:
		list = v.Users
	case *tg.MessagesChannelMessages:
		list = v.Users
	}
	out := make(map[int64]*tg.User, len(list))
	for _, item := range list {
		if user, ok := item.(*tg.User); ok {
			out[user.ID] = user
		}
	}
	return out
}

// mergeUsers 合并多次响应里的用户，后到的覆盖先到的。
func mergeUsers(dst, src map[int64]*tg.User) {
	for id, user := range src {
		dst[id] = user
	}
}

// chatPhotoOf 返回指定会话的头像照片 ID；没有头像时返回 0。
func chatPhotoOf(res tg.MessagesMessagesClass, chatID int64) int64 {
	for _, chat := range chatsOf(res) {
		switch c := chat.(type) {
		case *tg.Channel:
			if c.ID == chatID {
				return chatPhotoID(c.Photo)
			}
		case *tg.Chat:
			if c.ID == chatID {
				return chatPhotoID(c.Photo)
			}
		}
	}
	return 0
}

func chatPhotoID(photo tg.ChatPhotoClass) int64 {
	if p, ok := photo.(*tg.ChatPhoto); ok {
		return p.PhotoID
	}
	return 0
}

func userPhotoID(user *tg.User) int64 {
	if p, ok := user.Photo.(*tg.UserProfilePhoto); ok {
		return p.PhotoID
	}
	return 0
}

// groupLike 判断是否需要展示群头像与发送者（普通群、超级群；不含广播频道）。
func groupLike(p Peer) bool {
	return p.Kind == PeerKindGroup || (p.Kind == PeerKindChannel && !p.Broadcast)
}

// senderOf 从消息的 FromID 找到发送者资料与头像。
func senderOf(msg *tg.Message, users map[int64]*tg.User) *Sender {
	from, ok := msg.FromID.(*tg.PeerUser)
	if !ok || from == nil {
		return nil
	}
	user, ok := users[from.UserID]
	if !ok || user == nil {
		return nil
	}
	peer := peerFromUser(user)
	sender := &Sender{
		Peer:     peer,
		Name:     peer.Title,
		Username: peer.Username,
		Self:     peer.Self,
	}
	if photoID := userPhotoID(user); photoID != 0 {
		sender.Avatar = &PeerPhoto{Peer: peer, PhotoID: photoID}
	}
	return sender
}

// peerFromResolved 从 contacts.resolveUsername 的结果里取出会话与标题。
func peerFromResolved(resolved *tg.ContactsResolvedPeer) (Peer, error) {
	if resolved == nil {
		return Peer{}, errors.New("Telegram 没有返回会话信息")
	}

	switch peer := resolved.Peer.(type) {
	case *tg.PeerChannel:
		for _, chat := range resolved.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == peer.ChannelID {
				return peerFromChannel(ch), nil
			}
		}
		return Peer{}, fmt.Errorf("频道 %d 不在返回结果里", peer.ChannelID)
	case *tg.PeerChat:
		for _, chat := range resolved.Chats {
			if c, ok := chat.(*tg.Chat); ok && c.ID == peer.ChatID {
				return peerFromChat(c), nil
			}
		}
		return Peer{}, fmt.Errorf("群组 %d 不在返回结果里", peer.ChatID)
	case *tg.PeerUser:
		for _, user := range resolved.Users {
			if u, ok := user.(*tg.User); ok && u.ID == peer.UserID {
				return peerFromUser(u), nil
			}
		}
		return Peer{}, fmt.Errorf("用户 %d 不在返回结果里", peer.UserID)
	}
	return Peer{}, errors.New("链接指向的会话类型暂不支持")
}

// peerFromChatClass 把 chats 里的会话转成 Peer。
func peerFromChatClass(chat tg.ChatClass) (Peer, bool) {
	switch c := chat.(type) {
	case *tg.Channel:
		return peerFromChannel(c), true
	case *tg.Chat:
		return peerFromChat(c), true
	}
	return Peer{}, false
}

func peerFromChannel(ch *tg.Channel) Peer {
	return Peer{
		Kind:       PeerKindChannel,
		ID:         ch.ID,
		AccessHash: ch.AccessHash,
		Title:      ch.Title,
		Username:   ch.Username,
		Broadcast:  ch.Broadcast,
	}
}

func peerFromChat(chat *tg.Chat) Peer {
	return Peer{Kind: PeerKindGroup, ID: chat.ID, Title: chat.Title}
}

func peerFromUser(user *tg.User) Peer {
	return Peer{
		Kind:       PeerKindUser,
		ID:         user.ID,
		AccessHash: user.AccessHash,
		Title:      userDisplayName(user),
		Username:   user.Username,
		Self:       user.Self,
	}
}

// userDisplayName 依次退到姓名、用户名、ID。
func userDisplayName(user *tg.User) string {
	if name := strings.TrimSpace(user.FirstName + " " + user.LastName); name != "" {
		return name
	}
	if user.Username != "" {
		return "@" + user.Username
	}
	return fmt.Sprintf("user:%d", user.ID)
}

// inputPeerFor 用会话列表响应里的 chats/users 还原入口 peer，用于分页游标。
func inputPeerFor(peer tg.PeerClass, chats []tg.ChatClass, users []tg.UserClass) (tg.InputPeerClass, bool) {
	switch p := peer.(type) {
	case *tg.PeerChannel:
		for _, chat := range chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == p.ChannelID {
				return &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, true
			}
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, true
	case *tg.PeerUser:
		for _, user := range users {
			if u, ok := user.(*tg.User); ok && u.ID == p.UserID {
				return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, true
			}
		}
	}
	return nil, false
}

// messageDate 在会话列表附带的消息里查 top message 的时间，用于分页游标。
func messageDate(messages []tg.MessageClass, id int) int {
	for _, item := range messages {
		if msg, ok := item.(*tg.Message); ok && msg.ID == id {
			return msg.Date
		}
	}
	return 0
}

// peerKey 区分不同会话类型，避免用户/群/频道的 ID 撞在一起。
func peerKey(kind PeerKind, id int64) string {
	return string(kind) + ":" + strconv.FormatInt(id, 10)
}
