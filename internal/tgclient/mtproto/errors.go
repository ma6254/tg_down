package mtproto

import (
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tgerr"
)

// friendlyError 把 Telegram RPC 错误翻译成可操作的中文提示。
func friendlyError(err error) error {
	if err == nil {
		return nil
	}

	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("Telegram 限流，请等待 %s 后重试", d.Round(time.Second))
	}

	rpc, ok := tgerr.As(err)
	if !ok {
		return err
	}

	switch rpc.Type {
	case "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY":
		return errors.New("验证码错误")
	case "PHONE_CODE_EXPIRED":
		return errors.New("验证码已过期，请重新登录")
	case "PASSWORD_HASH_INVALID":
		return errors.New("两步验证密码错误")
	case "PHONE_NUMBER_INVALID":
		return errors.New("手机号无效")
	case "PHONE_NUMBER_BANNED":
		return errors.New("该手机号已被封禁")
	case "PHONE_NUMBER_UNOCCUPIED":
		return errors.New("该手机号尚未注册 Telegram")
	case "API_ID_INVALID", "API_ID_PUBLISHED_FLOOD":
		return errors.New("api_id / api_hash 无效，请到 https://my.telegram.org 核对")
	case "AUTH_KEY_UNREGISTERED":
		return errors.New("登录状态已失效，请删除 session 文件后重新登录")
	}

	return fmt.Errorf("%s（code %d）", rpc.Type, rpc.Code)
}
