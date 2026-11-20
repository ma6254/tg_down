package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/gotd/td/tg"
)

// AvatarThumb 返回当前账号头像的缩略图定位信息与 Telegram user.id。
//
// ok=false 表示账号没有设置头像（或只有被隐藏的 emoji 头像）。
// 调用方拿到 Thumb 后可以用 ThumbnailFetcher 下载并缓存字节；
// userID 用来把头像缓存按 Telegram 用户 ID 归档（换手机号也不变）。
func (s Session) AvatarThumb(ctx context.Context) (Thumb, int64, bool, error) {
	opts := s.options()
	if opts.APIID <= 0 || opts.APIHash == "" {
		return Thumb{}, 0, false, errors.New("缺少 api 凭据")
	}
	if opts.SessionPath == "" {
		return Thumb{}, 0, false, errors.New("缺少 session 路径")
	}
	if _, err := os.Stat(opts.SessionPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Thumb{}, 0, false, fmt.Errorf("未找到 session 文件 %s，请先登录", opts.SessionPath)
		}
		return Thumb{}, 0, false, fmt.Errorf("读取 session 文件: %w", err)
	}

	client, err := newClient(opts)
	if err != nil {
		return Thumb{}, 0, false, err
	}

	var (
		thumb  Thumb
		userID int64
		ok     bool
	)
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
		userID = self.ID
		profile, hasPhoto := self.Photo.(*tg.UserProfilePhoto)
		if !hasPhoto || profile.PhotoID == 0 {
			// 没有头像或头像被隐藏，属于正常情况；userID 仍然返回。
			return nil
		}

		photos, err := client.API().PhotosGetUserPhotos(ctx, &tg.PhotosGetUserPhotosRequest{
			UserID: &tg.InputUserSelf{},
			Offset: 0,
			MaxID:  0,
			Limit:  1,
		})
		if err != nil {
			return friendlyError(fmt.Errorf("读取头像: %w", err))
		}

		var list []tg.PhotoClass
		switch v := photos.(type) {
		case *tg.PhotosPhotos:
			list = v.Photos
		case *tg.PhotosPhotosSlice:
			list = v.Photos
		}
		for _, item := range list {
			photo, isPhoto := item.(*tg.Photo)
			if !isPhoto || photo == nil {
				continue
			}
			sizeType, width, height := pickThumbSize(photo.Sizes)
			if sizeType == "" {
				continue
			}
			thumb = Thumb{
				Kind:          ThumbKindPhoto,
				FileID:        photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				SizeType:      sizeType,
				DCID:          photo.DCID,
				Width:         width,
				Height:        height,
			}
			ok = true
			return nil
		}
		return nil
	})
	if err != nil {
		return Thumb{}, 0, false, err
	}
	return thumb, userID, ok, nil
}
