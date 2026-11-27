// Package msgcache 缓存链接预览用到的消息快照。
//
// 缓存只服务展示与历史回溯，不服务下载：下载前仍然必须用 MessageID 重新取一次
// 消息拿新的 file_reference（见 mtproto.Media 的说明）。
//
// 唯一键是 (chat_id, message_id)；快照只包含白名单字段，带结构版本号，
// 版本不匹配的行按未命中处理。过期不是删除：命中过期行会由调用方同步重取，
// 重取失败时可以降级返回旧快照并标记 Stale。
package msgcache

import (
	"encoding/json"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

const (
	// PayloadVersion 是快照结构版本；改动 snapshot 的字段时必须递增。
	PayloadVersion = 1
	// TTL 是正常快照的有效期；过期后由调用方同步重取。
	TTL = 24 * time.Hour
	// GoneTTL 是「消息不存在」结论的有效期。
	//
	// 比 TTL 短得多：消息可能只是当前账号暂时没权限，也可能被重新发布。
	GoneTTL = 10 * time.Minute
)

// snapshot 是落库的快照结构。
//
// 刻意不含 LinkResult.Err：错误没有稳定的序列化形式，而且失败结论由
// database.MessageCache.Status 表达。Cached/Stale/Gone 是运行时标记，不入库。
type snapshot struct {
	V              int                     `json:"v"`
	Target         link.Target             `json:"target"`
	Chat           mtproto.Peer            `json:"chat"`
	ChatAvatar     *mtproto.PeerPhoto      `json:"chat_avatar,omitempty"`
	Sender         *mtproto.Sender         `json:"sender,omitempty"`
	MessageID      int                     `json:"message_id"`
	Text           string                  `json:"text,omitempty"`
	Links          []mtproto.MessageLink   `json:"links,omitempty"`
	Entities       []mtproto.MessageEntity `json:"entities,omitempty"`
	Media          *mtproto.Media          `json:"media,omitempty"`
	Medias         []*mtproto.Media        `json:"medias,omitempty"`
	CommentSection *mtproto.CommentSection `json:"comment_section,omitempty"`
}

// Entry 是一条缓存命中。
type Entry struct {
	Result mtproto.LinkResult
	Status string
	// Fresh 表示还没过期；false 时调用方应当重取，重取失败才降级使用。
	Fresh bool
	// FetchedAt 是这份快照从 Telegram 取到的时间，用于向用户说明「缓存于何时」。
	FetchedAt time.Time
}

// Cache 把消息快照、会话定位与评论线程的仓库和 TTL 策略组合起来。
type Cache struct {
	msgs    *database.MessageStore
	peers   *database.PeerStore
	threads *database.ThreadStore
	now     func() time.Time
}

// New 创建缓存。now 默认取 time.Now，测试可以替换。
func New(db *database.DB) *Cache {
	return &Cache{
		msgs:    database.NewMessageStore(db),
		peers:   database.NewPeerStore(db),
		threads: database.NewThreadStore(db),
		now:     time.Now,
	}
}

// Lookup 读取一批消息的缓存。
//
// accountID 只用于 gone 的可见性判断：另一个账号确认的「不可见」对当前账号
// 未必成立，这种情况按未命中处理，避免把别人眼里的删除当成事实。
func (c *Cache) Lookup(chatID, accountID int64, ids []int) (map[int]Entry, error) {
	rows, err := c.msgs.Lookup(chatID, ids)
	if err != nil {
		return nil, err
	}

	now := c.now()
	out := make(map[int]Entry, len(rows))
	for id, row := range rows {
		if row.SchemaVer != PayloadVersion {
			continue
		}
		if row.Status == database.MessageStatusGone && row.StatusAccount != accountID {
			continue
		}

		result, ok := decode(row.Payload)
		if !ok && row.Status != database.MessageStatusGone {
			continue
		}
		result.MessageID = id
		out[id] = Entry{
			Result:    result,
			Status:    row.Status,
			Fresh:     now.Before(row.ExpiresAt),
			FetchedAt: row.FetchedAt,
		}
	}
	return out, nil
}

// Save 写入一批成功取到的快照。
func (c *Cache) Save(chatID, accountID int64, results []mtproto.LinkResult) error {
	if len(results) == 0 {
		return nil
	}
	now := c.now()
	rows := make([]database.MessageCache, 0, len(results))
	for _, result := range results {
		payload, err := encode(result)
		if err != nil || payload == "" {
			// 单条编码失败不该拖垮整批：跳过即可，下次预览会重新取。
			continue
		}
		rows = append(rows, database.MessageCache{
			ChatID:        chatID,
			MessageID:     result.MessageID,
			Status:        database.MessageStatusOK,
			StatusAccount: accountID,
			Payload:       payload,
			SchemaVer:     PayloadVersion,
			FetchedAt:     now,
			ExpiresAt:     now.Add(TTL),
			LastUsedAt:    now,
		})
	}
	return c.msgs.Save(rows)
}

// MarkGone 记录「消息不存在」的结论，保留已有快照正文。
func (c *Cache) MarkGone(chatID, accountID int64, ids []int) error {
	if len(ids) == 0 {
		return nil
	}
	now := c.now()
	rows := make([]database.MessageCache, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, database.MessageCache{
			ChatID:        chatID,
			MessageID:     id,
			Status:        database.MessageStatusGone,
			StatusAccount: accountID,
			SchemaVer:     PayloadVersion,
			FetchedAt:     now,
			ExpiresAt:     now.Add(GoneTTL),
			LastUsedAt:    now,
		})
	}
	return c.msgs.MarkGone(rows)
}

// Touch 更新命中行的最近使用时间。
func (c *Cache) Touch(chatID int64, ids []int) error {
	return c.msgs.Touch(chatID, ids, c.now())
}

// Prune 删除最近使用时间早于 before 的缓存行，单次最多 limit 行。
func (c *Cache) Prune(before time.Time, limit int) (int64, error) {
	return c.msgs.Prune(before, limit)
}

// encode 把 LinkResult 裁成白名单快照。
func encode(result mtproto.LinkResult) (string, error) {
	if result.MessageID <= 0 {
		return "", nil
	}
	body, err := json.Marshal(snapshot{
		V:              PayloadVersion,
		Target:         result.Target,
		Chat:           result.Chat,
		ChatAvatar:     result.ChatAvatar,
		Sender:         result.Sender,
		MessageID:      result.MessageID,
		Text:           result.Text,
		Links:          result.Links,
		Entities:       result.Entities,
		Media:          result.Media,
		Medias:         result.Medias,
		CommentSection: result.CommentSection,
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// decode 还原快照；结构版本不匹配或 JSON 非法时返回 ok=false。
func decode(payload string) (mtproto.LinkResult, bool) {
	if payload == "" {
		return mtproto.LinkResult{}, false
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		return mtproto.LinkResult{}, false
	}
	if snap.V != PayloadVersion {
		return mtproto.LinkResult{}, false
	}
	return mtproto.LinkResult{
		Target:         snap.Target,
		Chat:           snap.Chat,
		ChatAvatar:     snap.ChatAvatar,
		Sender:         snap.Sender,
		MessageID:      snap.MessageID,
		Text:           snap.Text,
		Links:          snap.Links,
		Entities:       snap.Entities,
		Media:          snap.Media,
		Medias:         snap.Medias,
		CommentSection: snap.CommentSection,
	}, true
}

// Stats 是本地缓存的规模。
type Stats struct {
	Messages int64
	Peers    int64
	Threads  int64
}

// Stats 统计各表的缓存行数。
func (c *Cache) Stats() (Stats, error) {
	var stats Stats
	var err error
	if stats.Messages, err = c.msgs.Count(); err != nil {
		return Stats{}, err
	}
	if stats.Peers, err = c.peers.Count(); err != nil {
		return Stats{}, err
	}
	if stats.Threads, err = c.threads.Count(); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

// Clear 清空全部缓存，返回被删除的行数。
//
// 只删数据库行：磁盘上的缩略图缓存有独立生命周期，不在这里动。
func (c *Cache) Clear() (Stats, error) {
	var removed Stats
	var err error
	if removed.Messages, err = c.msgs.Clear(); err != nil {
		return Stats{}, err
	}
	if removed.Peers, err = c.peers.Clear(); err != nil {
		return Stats{}, err
	}
	if removed.Threads, err = c.threads.Clear(); err != nil {
		return Stats{}, err
	}
	return removed, nil
}
