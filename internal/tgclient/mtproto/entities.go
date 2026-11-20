package mtproto

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

// MessageLink 是消息正文里的一段链接。
//
// Text 是可见文字（纯 URL 实体时与 URL 相同）；URL 是实际目标地址。
// 频道帖里常见的「第 1 集 | 第 2 集」就是 text_link，指向评论区的具体消息。
type MessageLink struct {
	Text string
	URL  string
}

// messageLinks 提取正文中的链接实体，按出现顺序去重。
//
// Telegram 实体的 Offset/Length 以 UTF-16 码元计，这里统一换算后再取正文片段，
// 避免中文、emoji 之类非单字节字符把片段切错。
func messageLinks(msg *tg.Message) []MessageLink {
	entities, ok := msg.GetEntities()
	if !ok || len(entities) == 0 {
		return nil
	}

	units := utf16.Encode([]rune(msg.GetMessage()))
	seen := make(map[string]struct{}, len(entities))
	links := make([]MessageLink, 0, len(entities))

	for _, entity := range entities {
		var link MessageLink
		switch e := entity.(type) {
		case *tg.MessageEntityTextURL:
			link = MessageLink{Text: utf16Slice(units, e.Offset, e.Length), URL: e.URL}
		case *tg.MessageEntityURL:
			text := utf16Slice(units, e.Offset, e.Length)
			link = MessageLink{Text: text, URL: text}
		default:
			continue
		}

		if link.URL == "" {
			continue
		}
		if _, ok := seen[link.URL]; ok {
			continue
		}
		seen[link.URL] = struct{}{}
		links = append(links, link)
	}
	return links
}

// utf16Slice 按 UTF-16 码元偏移截取字符串；越界时返回空串。
func utf16Slice(units []uint16, offset, length int) string {
	if offset < 0 || length <= 0 || offset+length > len(units) {
		return ""
	}
	return string(utf16.Decode(units[offset : offset+length]))
}

// MessageEntity 是消息正文里的一段富文本标注。
//
// Type 沿用 Bot API 的实体名（bold、italic、text_link、custom_emoji……），
// 前端按类型决定怎么渲染；Offset/Length 以 UTF-16 码元计，与 Telegram 的原始实体一致。
// 只有对应类型才会填 URL、UserID、Language 等附加字段，其余留零值。
type MessageEntity struct {
	Type       string
	Offset     int
	Length     int
	URL        string // text_link
	UserID     int64  // text_mention
	Language   string // pre 的语言标记
	DocumentID int64  // custom_emoji 对应的贴纸文档
	Collapsed  bool   // blockquote 默认折叠（Bot API 的 expandable_blockquote）
	Date       int    // date_time：Unix 时间戳
	DateFormat string // date_time：relative | time | time_long | date | date_long | weekday
}

// entityKey 用于去掉完全重复的实体（某些消息会重复下发同一条链接实体）。
type entityKey struct {
	typ        string
	offset     int
	length     int
	url        string
	userID     int64
	documentID int64
	language   string
	collapsed  bool
	date       int
	dateFormat string
}

// messageEntities 把 MTProto 实体转换成与前端无关的 DTO。
//
// 越界或空实体直接丢弃（例如被服务端截断的自动识别链接），
// 结果按位置排序：起点升序、跨度降序，便于按「外层先、内层后」渲染。
func messageEntities(msg *tg.Message) []MessageEntity {
	entities, ok := msg.GetEntities()
	if !ok || len(entities) == 0 {
		return nil
	}
	total := len(utf16.Encode([]rune(msg.GetMessage())))

	out := make([]MessageEntity, 0, len(entities))
	seen := make(map[entityKey]struct{}, len(entities))
	for _, entity := range entities {
		var e MessageEntity
		switch v := entity.(type) {
		case *tg.MessageEntityBold:
			e = MessageEntity{Type: "bold", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityItalic:
			e = MessageEntity{Type: "italic", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityUnderline:
			e = MessageEntity{Type: "underline", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityStrike:
			e = MessageEntity{Type: "strikethrough", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntitySpoiler:
			e = MessageEntity{Type: "spoiler", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityCode:
			e = MessageEntity{Type: "code", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityPre:
			e = MessageEntity{Type: "pre", Offset: v.Offset, Length: v.Length, Language: v.Language}
		case *tg.MessageEntityBlockquote:
			e = MessageEntity{Type: "blockquote", Offset: v.Offset, Length: v.Length, Collapsed: v.Collapsed}
		case *tg.MessageEntityTextURL:
			e = MessageEntity{Type: "text_link", Offset: v.Offset, Length: v.Length, URL: v.URL}
		case *tg.MessageEntityURL:
			e = MessageEntity{Type: "url", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityEmail:
			e = MessageEntity{Type: "email", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityPhone:
			e = MessageEntity{Type: "phone_number", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityMention:
			e = MessageEntity{Type: "mention", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityMentionName:
			e = MessageEntity{Type: "text_mention", Offset: v.Offset, Length: v.Length, UserID: v.UserID}
		case *tg.MessageEntityHashtag:
			e = MessageEntity{Type: "hashtag", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityCashtag:
			e = MessageEntity{Type: "cashtag", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityBotCommand:
			e = MessageEntity{Type: "bot_command", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityBankCard:
			e = MessageEntity{Type: "bank_card", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityTonAddress:
			e = MessageEntity{Type: "ton_address", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityCustomEmoji:
			e = MessageEntity{Type: "custom_emoji", Offset: v.Offset, Length: v.Length, DocumentID: v.DocumentID}
		case *tg.MessageEntityFormattedDate:
			e = MessageEntity{
				Type:       "date_time",
				Offset:     v.Offset,
				Length:     v.Length,
				Date:       v.Date,
				DateFormat: formattedDateLayout(v),
			}
		case *tg.MessageEntityDiffInsert:
			e = MessageEntity{Type: "diff_insert", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityDiffDelete:
			e = MessageEntity{Type: "diff_delete", Offset: v.Offset, Length: v.Length}
		case *tg.MessageEntityDiffReplace:
			e = MessageEntity{Type: "diff_replace", Offset: v.Offset, Length: v.Length}
		default:
			// 不认识的构造函数直接跳过：正文仍按纯文本展示，不会丢字。
			continue
		}

		if e.Length <= 0 || e.Offset < 0 || e.Offset >= total {
			continue
		}
		if e.Offset+e.Length > total {
			e.Length = total - e.Offset
		}

		key := entityKey{
			typ:        e.Type,
			offset:     e.Offset,
			length:     e.Length,
			url:        e.URL,
			userID:     e.UserID,
			documentID: e.DocumentID,
			language:   e.Language,
			collapsed:  e.Collapsed,
			date:       e.Date,
			dateFormat: e.DateFormat,
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Offset != out[j].Offset {
			return out[i].Offset < out[j].Offset
		}
		if out[i].Length != out[j].Length {
			return out[i].Length > out[j].Length
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// formattedDateLayout 把 date_time 的互斥格式标记折叠成一个枚举，交给前端本地化。
func formattedDateLayout(e *tg.MessageEntityFormattedDate) string {
	switch {
	case e.Relative:
		return "relative"
	case e.LongTime:
		return "time_long"
	case e.ShortTime:
		return "time"
	case e.LongDate:
		return "date_long"
	case e.ShortDate:
		return "date"
	case e.DayOfWeek:
		return "weekday"
	}
	return ""
}

// trimMessage 去掉正文首尾空白，并把实体偏移同步平移。
//
// Telegram 的偏移以原始正文为基准，直接 TrimSpace 会让后续切片整体错位；
// 这里按「裁掉的 UTF-16 码元数」平移并裁剪跨界的实体，裁完为空的实体丢弃。
func trimMessage(text string, entities []MessageEntity) (string, []MessageEntity) {
	trimmed := strings.TrimSpace(text)
	if trimmed == text || len(entities) == 0 {
		return trimmed, entities
	}

	prefix := text[:len(text)-len(strings.TrimLeftFunc(text, unicode.IsSpace))]
	lead := len(utf16.Encode([]rune(prefix)))
	total := len(utf16.Encode([]rune(trimmed)))

	out := make([]MessageEntity, 0, len(entities))
	for _, e := range entities {
		start, end := e.Offset-lead, e.Offset-lead+e.Length
		if start < 0 {
			start = 0
		}
		if end > total {
			end = total
		}
		if start >= end {
			continue
		}
		e.Offset, e.Length = start, end-start
		out = append(out, e)
	}
	return trimmed, out
}
