package xiaohongshu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

// NotificationReplyResult 通知回复的结果。
type NotificationReplyResult struct {
	CommentID        string `json:"comment_id"`
	TargetCommentID  string `json:"target_comment_id"`
	Status           string `json:"status"`
	PublicVisibility string `json:"public_visibility"`
	RetrySafe        bool   `json:"retry_safe"`
	Nickname         string `json:"nickname"`
	FeedID           string `json:"feed_id,omitempty"`
	Content          string `json:"content"`
}

// Reply 回复一条评论。
func (n *NotificationAction) Reply(ctx context.Context, commentID, content string) (*NotificationReplyResult, error) {
	if strings.TrimSpace(commentID) == "" {
		return nil, fmt.Errorf("缺少 comment_id")
	}
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("回复内容不能为空")
	}

	page := n.page.Context(ctx).Timeout(3 * time.Minute)

	page.MustNavigate("https://www.xiaohongshu.com/notification").MustWaitLoad()
	humanize.Delay(ctx, humanize.AfterNavigate)

	target, index, err := n.locate(ctx, page, commentID)
	if err != nil {
		return nil, err
	}

	items, err := page.Elements(`.tabs-content-container > .container`)
	if err != nil {
		return nil, fmt.Errorf("未找到通知条目: %w", err)
	}
	if index >= len(items) {
		return nil, fmt.Errorf("通知条目渲染数(%d)少于目标位置(%d)，页面可能未加载完", len(items), index)
	}
	item := items[index]
	// State-list and DOM ordering can diverge; do not reply to an unrelated row.
	itemText, err := item.Text()
	if err != nil || target.from().Nickname == "" || target.Comment.Content == "" || !strings.Contains(itemText, target.from().Nickname) || !strings.Contains(itemText, target.Comment.Content) {
		return nil, commentNotSubmitted(fmt.Errorf("通知条目与目标评论不符，无法安全定位回复"))
	}

	humanize.Delay(ctx, humanize.Reading)

	replyBtn, err := item.Element(`.action-reply`)
	if err != nil {
		return nil, fmt.Errorf("该通知没有回复入口（评论可能已删除或不可回复）: %w", err)
	}

	humanize.Delay(ctx, humanize.BeforeClick)
	if err := humanize.Click(replyBtn); err != nil {
		return nil, fmt.Errorf("无法点击回复: %w", err)
	}
	humanize.Delay(ctx, humanize.AfterClick)

	input, err := visibleCommentInput(ctx, func() (rod.Elements, error) { return item.Elements(notificationComposerSelector) }, 5*time.Second)
	if err != nil {
		return nil, commentNotSubmitted(fmt.Errorf("回复输入框未出现: %w", err))
	}

	if err := verifyReplyTarget(input, target.from().Nickname); err != nil {
		return nil, commentNotSubmitted(err)
	}

	if err := humanize.Type(ctx, input, content); err != nil {
		return nil, fmt.Errorf("无法输入回复内容: %w", err)
	}
	humanize.Delay(ctx, humanize.AfterType)

	submit, err := item.Element(`button.submit`)
	if err != nil {
		return nil, fmt.Errorf("未找到发送按钮: %w", err)
	}

	humanize.Delay(ctx, humanize.BeforeSubmit)
	if err := verifyReplyTarget(input, target.from().Nickname); err != nil {
		return nil, commentNotSubmitted(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, commentNotSubmitted(err)
	}
	responses, stopWatching := watchCommentResponse(page, target.Item.ID, commentID, content)
	defer stopWatching()
	if err := humanize.Click(submit); err != nil {
		return nil, commentUnconfirmed(fmt.Errorf("无法确认发送点击: %w", err))
	}

	newID, err := waitCommentAcknowledgement(ctx, responses, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if target.Item.ID == "" || target.Item.XsecToken == "" {
		return nil, commentUnconfirmed(fmt.Errorf("接口已接收，但没有可用于复核的笔记链接"))
	}
	if err := page.Navigate(makeFeedDetailURL(target.Item.ID, target.Item.XsecToken)); err != nil {
		return nil, commentUnconfirmed(err)
	}
	if err := page.WaitLoad(); err != nil {
		return nil, commentUnconfirmed(err)
	}
	if err := waitPersistedComment(ctx, page, nil, content, "", newID, 10*time.Second); err != nil {
		return nil, err
	}

	humanize.Delay(ctx, humanize.AfterInteract)

	logrus.Infof("通知回复成功: comment=%s", commentID)
	return &NotificationReplyResult{
		CommentID:        newID,
		TargetCommentID:  commentID,
		Status:           "visible_to_current_account",
		PublicVisibility: "unverified",
		RetrySafe:        false,
		Nickname:         target.from().Nickname,
		FeedID:           target.Item.ID,
		Content:          content,
	}, nil
}

// locate 找到目标评论在当前分区里的位置，必要时滚动加载。
func (n *NotificationAction) locate(ctx context.Context, page *rod.Page, commentID string) (*rawNotification, int, error) {
	const maxRounds = 20

	for range maxRounds {
		payload, err := n.readTab(page, TabMentions)
		if err != nil {
			return nil, 0, err
		}

		for i, r := range payload.MessageList {
			if r.Comment.ID != commentID {
				continue
			}
			if !r.visible() {
				return nil, 0, fmt.Errorf("该评论已删除或不可见，不能回复: %s", commentID)
			}
			return &r, i, nil
		}

		if !payload.HasMore {
			return nil, 0, fmt.Errorf("未找到评论 %s，它可能不在「评论和@」里或已被清理", commentID)
		}

		if err := page.Mouse.Scroll(0, 800, 5); err != nil {
			return nil, 0, fmt.Errorf("滚动查找失败: %w", err)
		}
		humanize.Delay(ctx, humanize.BetweenScroll)

		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
	}
	return nil, 0, fmt.Errorf("翻找 %d 轮仍未定位到评论 %s", maxRounds, commentID)
}

// verifyReplyTarget 用输入框的 placeholder 核对回复对象。
func verifyReplyTarget(input *rod.Element, nickname string) error {
	placeholder, err := commentInputPlaceholder(input)
	if err != nil || placeholder == "" {
		return fmt.Errorf("读不到回复框提示文字，无法确认回复对象，已中止")
	}
	if !replyPlaceholderMatches(placeholder, nickname) {
		return fmt.Errorf("回复对象不符：期望 %q，实际提示为 %q，已中止", nickname, placeholder)
	}
	return nil
}
