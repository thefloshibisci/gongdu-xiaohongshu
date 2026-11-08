package main

import (
	"context"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func (s *XiaohongshuService) FollowUser(ctx context.Context, userID, token string, backOnly bool) (*xiaohongshu.FollowResult, error) {
	if err := xiaohongshu.ValidateFollowIdentity(userID, token); err != nil {
		return nil, err
	}
	b := newBrowser()
	defer b.Close()
	page := b.NewPage()
	defer page.Close()
	return xiaohongshu.FollowUser(ctx, page, userID, token, backOnly)
}
