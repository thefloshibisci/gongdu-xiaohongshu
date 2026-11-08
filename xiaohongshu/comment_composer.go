package xiaohongshu

import (
	"context"
	"fmt"
	"github.com/go-rod/rod"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
	"time"
)

// A collapsed composer already contains a focusable contenteditable. Focusing
// and typing alone does not trigger the UI click handler that reveals Send.
func activateFeedComposer(ctx context.Context, page *rod.Page) error {
	elements, err := page.Elements("div.input-box div.content-edit .not-active span")
	if err != nil {
		return err
	}
	var openers rod.Elements
	for _, e := range elements {
		visible, err := e.Visible()
		if err != nil {
			return err
		}
		if visible && humanize.Visible(e) {
			openers = append(openers, e)
		}
	}
	if len(openers) > 1 {
		return fmt.Errorf("出现多个评论展开入口，已中止")
	}
	if len(openers) == 1 {
		if err := humanize.Click(openers[0].Timeout(5 * time.Second)); err != nil {
			return fmt.Errorf("展开评论输入框失败: %w", err)
		}
		humanize.Delay(ctx, humanize.AfterClick)
	}
	return ctx.Err()
}

// Reject an inactive/hidden/disabled button before the submission boundary.
// Once the actual click starts, existing unconfirmed/no-retry semantics apply.
func readyFeedSubmit(page *rod.Page) (*rod.Element, error) {
	elements, err := page.Elements("div.bottom button.submit")
	if err != nil {
		return nil, err
	}
	var visible rod.Elements
	for _, e := range elements {
		shown, err := e.Visible()
		if err != nil {
			return nil, err
		}
		if shown && humanize.Visible(e) {
			visible = append(visible, e)
		}
	}
	if len(visible) != 1 {
		return nil, fmt.Errorf("可见发送按钮数量为 %d，评论框未展开或目标不明确", len(visible))
	}
	button := visible[0]
	if err := button.Timeout(3 * time.Second).WaitEnabled(); err != nil {
		return nil, fmt.Errorf("发送按钮不可用: %w", err)
	}
	if _, err := button.Timeout(3 * time.Second).WaitInteractable(); err != nil {
		return nil, fmt.Errorf("发送按钮不可点击: %w", err)
	}
	return button, nil
}
