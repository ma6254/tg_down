package mtproto

import "context"

// Session 描述一次 MTProto 连接所需的公共参数：应用身份、session 文件与代理。
//
// 命令层只需要组装一次 Session，再调用 Login / Probe / Profile，
// 不用到处重复填 LoginOptions。
type Session struct {
	App         App
	SessionPath string
	Proxy       string
}

// options 把 Session 转换成底层参数。
func (s Session) options() LoginOptions {
	return LoginOptions{
		APIID:       s.App.ID,
		APIHash:     s.App.Hash,
		SessionPath: s.SessionPath,
		Proxy:       s.Proxy,
	}
}

// Login 用当前 Session 登录；code / password 留空则交互输入。
func (s Session) Login(ctx context.Context, phone, code, password string) (*Account, error) {
	opts := s.options()
	opts.Phone = phone
	opts.Code = code
	opts.Password = password
	return Login(ctx, opts)
}

// Probe 检查 session 是否仍有效，并返回账号与已登录设备。
func (s Session) Probe(ctx context.Context) (*SessionInfo, error) {
	return Probe(ctx, s.options())
}

// Profile 查询账号的扩展资料（生日、简介），不落库。
func (s Session) Profile(ctx context.Context) (*ProfileInfo, error) {
	info, err := s.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	return info.Profile, nil
}

// Desktop 描述本机 Telegram Desktop 的 tdata 探测参数。
type Desktop struct {
	TDataPath string
	Passcode  string
	Proxy     string
}

// Probe 读取 tdata 并查询其中的账号。
func (d Desktop) Probe(ctx context.Context) (*DesktopProbeResult, error) {
	return ProbeDesktop(ctx, DesktopProbeOptions{
		TDataPath: d.TDataPath,
		Passcode:  []byte(d.Passcode),
		Proxy:     d.Proxy,
	})
}
