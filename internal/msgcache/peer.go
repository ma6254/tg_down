package msgcache

import (
	"strconv"
	"strings"
	"time"

	"github.com/ma6254/tg_down/internal/database"
	"github.com/ma6254/tg_down/internal/link"
	"github.com/ma6254/tg_down/internal/tgclient/mtproto"
)

// PeerTTL 是会话定位结果的有效期。
//
// 与消息缓存一致：用户名、频道标题、access_hash 都可能变，
// 一天后重新解析一次，保证标题和可见性不会长期过期。
const PeerTTL = 24 * time.Hour

// PeerKey 返回链接在会话缓存里的键。
//
// 评论链接定位到的是关联讨论组，和帖子链接落在不同 peer 上，所以键里要带标记；
// username 统一转小写，避免大小写差异导致命中不了。
func PeerKey(target link.Target) (string, bool) {
	comment := target.IsComment()
	switch {
	case target.Username != "":
		key := "u:" + strings.ToLower(target.Username)
		if comment {
			key += "#c"
		}
		return key, true
	case target.ChannelID != 0:
		key := "c:" + strconv.FormatInt(target.ChannelID, 10)
		if comment {
			key += "#c"
		}
		return key, true
	}
	return "", false
}

// PeerEntry 是一条待写入的会话定位结果。
type PeerEntry struct {
	Key  string
	Peer mtproto.Peer
}

// LoadPeers 批量读取会话定位结果，返回以链接下标索引的 map。
//
// 第二个返回值是写入时记录的单文件上限：全部命中时靠它算 Downloadable，
// 免得为了读 Premium 状态再连一次 Telegram。
func (c *Cache) LoadPeers(accountID int64, targets []link.Target) (map[int]mtproto.Peer, int64, error) {
	keys := make([]string, 0, len(targets))
	index := make(map[string]int, len(targets))
	for i, target := range targets {
		key, ok := PeerKey(target)
		if !ok {
			continue
		}
		if _, seen := index[key]; seen {
			continue
		}
		index[key] = i
		keys = append(keys, key)
	}

	rows, err := c.peers.LoadPeers(accountID, keys)
	if err != nil {
		return nil, 0, err
	}

	now := c.now()
	out := make(map[int]mtproto.Peer, len(rows))
	var maxSize int64
	for key, row := range rows {
		if now.After(row.ExpiresAt) {
			continue
		}
		i, ok := index[key]
		if !ok {
			continue
		}
		out[i] = peerFromRow(row)
		if row.MaxSize > maxSize {
			maxSize = row.MaxSize
		}
	}
	return out, maxSize, nil
}

// SavePeers 写入会话定位结果；键与账号一起构成主键。
func (c *Cache) SavePeers(accountID, maxSize int64, entries []PeerEntry) error {
	if len(entries) == 0 {
		return nil
	}
	now := c.now()
	rows := make([]database.PeerCache, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, database.PeerCache{
			AccountID:  accountID,
			LookupKey:  entry.Key,
			Kind:       string(entry.Peer.Kind),
			PeerID:     entry.Peer.ID,
			AccessHash: entry.Peer.AccessHash,
			Title:      entry.Peer.Title,
			Username:   entry.Peer.Username,
			Broadcast:  entry.Peer.Broadcast,
			Self:       entry.Peer.Self,
			MaxSize:    maxSize,
			FetchedAt:  now,
			ExpiresAt:  now.Add(PeerTTL),
		})
	}
	return c.peers.SavePeers(rows)
}

// ForgetPeers 删除失效的会话定位结果，下次预览会重新解析。
func (c *Cache) ForgetPeers(accountID int64, keys []string) error {
	_, err := c.peers.DeletePeers(accountID, keys)
	return err
}

// PrunePeers 清理过期的会话定位结果。
func (c *Cache) PrunePeers(limit int) (int64, error) {
	return c.peers.PruneExpired(c.now(), limit)
}

// peerFromRow 把数据库行还原成 Peer。
func peerFromRow(row database.PeerCache) mtproto.Peer {
	return mtproto.Peer{
		Kind:       mtproto.PeerKind(row.Kind),
		ID:         row.PeerID,
		AccessHash: row.AccessHash,
		Title:      row.Title,
		Username:   row.Username,
		Broadcast:  row.Broadcast,
		Self:       row.Self,
	}
}
