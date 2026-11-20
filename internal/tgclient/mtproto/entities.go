package mtproto

import (
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
