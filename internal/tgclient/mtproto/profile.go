package mtproto

import "github.com/gotd/td/tg"

// ProfileInfo 是 users.getFullUser 返回的扩展资料，只在内存中使用。
type ProfileInfo struct {
	About       string
	HasBirthday bool
	BirthDay    int
	BirthMonth  int
	BirthYear   int // 0 表示未填写年份
}

// profileFromFull 从 users.getFullUser 的结果里提取展示所需字段。
func profileFromFull(full *tg.UsersUserFull) *ProfileInfo {
	info := &ProfileInfo{About: full.FullUser.About}
	if birthday, ok := full.FullUser.GetBirthday(); ok {
		info.HasBirthday = true
		info.BirthDay = birthday.Day
		info.BirthMonth = birthday.Month
		info.BirthYear = birthday.Year
	}
	return info
}
