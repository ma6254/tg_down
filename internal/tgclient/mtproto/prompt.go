package mtproto

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"golang.org/x/term"
)

// prompter 实现 auth.UserAuthenticator：优先用参数里的值，
// 缺失时在终端交互输入，密码不回显。
type prompter struct {
	phone    string
	code     string
	password string

	in     io.Reader
	out    io.Writer
	reader *bufio.Reader
}

func newPrompter(o LoginOptions) *prompter {
	in := o.In
	if in == nil {
		in = os.Stdin
	}
	out := o.Out
	if out == nil {
		out = os.Stdout
	}
	return &prompter{
		phone:    o.Phone,
		code:     o.Code,
		password: o.Password,
		in:       in,
		out:      out,
		reader:   bufio.NewReader(in),
	}
}

// Phone 返回手机号，登录时已确定，交互中不会再问。
func (p *prompter) Phone(context.Context) (string, error) {
	return p.phone, nil
}

// Code 返回验证码；未通过 --code 提供时提示用户输入。
func (p *prompter) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	if p.code != "" {
		return p.code, nil
	}
	fmt.Fprint(p.out, "请输入验证码: ")
	code, err := p.readLine()
	if err != nil {
		return "", err
	}
	if code == "" {
		return "", errors.New("验证码不能为空")
	}
	return code, nil
}

// Password 返回两步验证密码；未通过 --password 提供时交互输入且不回显。
func (p *prompter) Password(context.Context) (string, error) {
	if p.password != "" {
		return p.password, nil
	}
	fmt.Fprint(p.out, "请输入两步验证密码（输入不回显）: ")
	password, err := p.readSecret()
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("两步验证密码不能为空")
	}
	return password, nil
}

// AcceptTermsOfService 直接接受服务条款，避免登录流程卡住。
func (p *prompter) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return nil
}

// SignUp 拒绝自动注册：本工具只登录已有账号。
func (p *prompter) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("该手机号尚未注册 Telegram，请先用官方客户端注册后再登录")
}

// readLine 读取一行并去掉首尾空白。
func (p *prompter) readLine() (string, error) {
	line, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取输入: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// readSecret 优先用不回显的方式读取密码；非终端环境退化为普通读取。
func (p *prompter) readSecret() (string, error) {
	if f, ok := p.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		password, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(p.out)
		if err != nil {
			return "", fmt.Errorf("读取密码: %w", err)
		}
		return strings.TrimSpace(string(password)), nil
	}
	return p.readLine()
}
