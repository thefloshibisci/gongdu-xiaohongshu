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

// CommentFeedAction 表示 Feed 评论动作
type CommentFeedAction struct {
	page *rod.Page
}

// NewCommentFeedAction 创建 Feed 评论动作
func NewCommentFeedAction(page *rod.Page) *CommentFeedAction {
	return &CommentFeedAction{page: page}
}

// PostComment 发表评论到 Feed
func (f *CommentFeedAction) PostComment(ctx context.Context, feedID, xsecToken, content string) error {
	if strings.TrimSpace(content) == "" {
		return commentNotSubmitted(fmt.Errorf("评论内容不能为空"))
	}
	page := f.page.Context(ctx).Timeout(90 * time.Second)

	url := makeFeedDetailURL(feedID, xsecToken)
	logrus.Infof("打开 feed 详情页: %s", url)

	// 导航到详情页
	page.MustNavigate(url)
	waitFeedPageReady(page)
	humanize.Delay(ctx, humanize.AfterNavigate)

	// 检测页面是否可访问
	if err := checkPageAccessible(page); err != nil {
		return err
	}

	if err := activateFeedComposer(ctx, page); err != nil {
		return commentNotSubmitted(err)
	}
	elem2, err := visibleCommentInput(ctx, func() (rod.Elements, error) { return page.Elements(feedComposerSelector) }, 5*time.Second)
	if err != nil {
		return commentNotSubmitted(err)
	}
	placeholder, err := commentInputPlaceholder(elem2)
	if err != nil {
		return commentNotSubmitted(err)
	}
	if strings.HasPrefix(placeholder, "回复") || strings.HasPrefix(strings.ToLower(placeholder), "reply to") {
		return commentNotSubmitted(fmt.Errorf("当前输入框处于回复模式，不会作为顶层评论提交"))
	}

	if err := humanize.Type(ctx, elem2, content); err != nil {
		logrus.Warnf("Failed to input comment content: %v", err)
		return commentNotSubmitted(fmt.Errorf("无法输入评论内容: %w", err))
	}

	humanize.Delay(ctx, humanize.AfterType)

	submitButton, err := readyFeedSubmit(page)
	if err != nil {
		logrus.Warnf("Failed to find submit button: %v", err)
		return commentNotSubmitted(fmt.Errorf("未找到提交按钮: %w", err))
	}

	before, err := renderedComments(page)
	if err != nil {
		return commentNotSubmitted(err)
	}
	if err := ctx.Err(); err != nil {
		return commentNotSubmitted(err)
	}
	placeholder, err = commentInputPlaceholder(elem2)
	if err != nil || strings.HasPrefix(placeholder, "回复") || strings.HasPrefix(strings.ToLower(placeholder), "reply to") {
		return commentNotSubmitted(fmt.Errorf("提交前输入框目标发生变化，已中止"))
	}
	responses, stopWatching := watchCommentResponse(page, feedID, "", content)
	defer stopWatching()
	if err := humanize.Click(submitButton); err != nil {
		logrus.Warnf("Failed to click submit button: %v", err)
		return commentUnconfirmed(fmt.Errorf("发送点击未确认完成: %w", err))
	}

	humanize.Delay(ctx, humanize.AfterClick)

	if err := waitNewComment(ctx, page, before, content, "", responses, 10*time.Second); err != nil {
		return err
	}

	logrus.Infof("Comment posted and verified to feed: %s", feedID)
	return nil
}

// ReplyToComment 回复指定评论
func (f *CommentFeedAction) ReplyToComment(ctx context.Context, feedID, xsecToken, commentID, userID, content string) error {
	if strings.TrimSpace(content) == "" {
		return commentNotSubmitted(fmt.Errorf("回复内容不能为空"))
	}
	if commentID == "" && userID == "" {
		return commentNotSubmitted(fmt.Errorf("缺少目标评论标识"))
	}
	page := f.page.Context(ctx).Timeout(5 * time.Minute)
	url := makeFeedDetailURL(feedID, xsecToken)
	logrus.Infof("打开 feed 详情页进行回复: %s", url)

	// 导航到详情页
	page.MustNavigate(url)
	waitFeedPageReady(page)
	humanize.Delay(ctx, humanize.AfterNavigate)

	// 检测页面是否可访问
	if err := checkPageAccessible(page); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	// 使用 Go 实现的查找逻辑
	commentEl, err := findCommentElement(ctx, page, commentID, userID)
	if err != nil {
		return fmt.Errorf("无法找到评论: %w", err)
	}

	// 滚动到评论位置
	logrus.Info("滚动到评论位置...")
	commentEl.MustScrollIntoView()
	humanize.Delay(ctx, humanize.BetweenScroll)

	nickname, err := feedReplyNickname(commentEl)
	if err != nil {
		return commentNotSubmitted(err)
	}
	threadValue, err := commentEl.Eval(`() => { const thread=this.closest('.parent-comment'); const first=thread && (thread.matches('.comment-item') ? thread : thread.querySelector('.comment-item')); return first ? (first.id || first.getAttribute('data-comment-id') || '') : (this.id || this.getAttribute('data-comment-id') || ''); }`)
	if err != nil || threadValue.Value.Str() == "" {
		return commentNotSubmitted(fmt.Errorf("无法确认目标评论所在楼层"))
	}
	threadID := threadValue.Value.Str()
	logrus.Info("准备点击回复按钮")

	// 查找并点击回复按钮
	replyBtn, err := commentEl.Element(".right .interactions .reply")
	if err != nil {
		return fmt.Errorf("无法找到回复按钮: %w", err)
	}

	if err := humanize.Click(replyBtn); err != nil {
		return fmt.Errorf("点击回复按钮失败: %w", err)
	}

	humanize.Delay(ctx, humanize.AfterClick)

	inputEl, err := visibleCommentInput(ctx, func() (rod.Elements, error) { return page.Elements(feedComposerSelector) }, 5*time.Second)
	if err != nil {
		return commentNotSubmitted(err)
	}
	if err := verifyReplyTarget(inputEl, nickname); err != nil {
		return commentNotSubmitted(err)
	}

	// 输入内容
	if err := humanize.Type(ctx, inputEl, content); err != nil {
		return commentNotSubmitted(fmt.Errorf("输入回复内容失败: %w", err))
	}

	humanize.Delay(ctx, humanize.AfterType)

	// 查找并点击提交按钮
	submitBtn, err := readyFeedSubmit(page)
	if err != nil {
		return commentNotSubmitted(fmt.Errorf("无法找到提交按钮: %w", err))
	}

	if err := verifyReplyTarget(inputEl, nickname); err != nil {
		return commentNotSubmitted(err)
	}
	before, err := renderedComments(page)
	if err != nil {
		return commentNotSubmitted(err)
	}
	if err := ctx.Err(); err != nil {
		return commentNotSubmitted(err)
	}
	targetValue, err := commentEl.Eval(`() => (this.id || this.getAttribute('data-comment-id') || '').replace(/^comment-/, '')`)
	if err != nil || targetValue.Value.Str() == "" {
		return commentNotSubmitted(fmt.Errorf("目标评论ID无法核实"))
	}
	responses, stopWatching := watchCommentResponse(page, feedID, targetValue.Value.Str(), content)
	defer stopWatching()
	if err := humanize.Click(submitBtn); err != nil {
		return commentUnconfirmed(fmt.Errorf("回复发送点击未确认完成: %w", err))
	}

	humanize.Delay(ctx, humanize.AfterClick)

	if err := waitNewComment(ctx, page, before, content, threadID, responses, 10*time.Second); err != nil {
		return err
	}

	logrus.Infof("回复评论成功并已确认")
	return nil
}

// findCommentElement 滚动查找指定评论：提供 commentID 时精确匹配，仅未提供时按 userID 匹配。
// 二级评论折叠在「展开 N 条回复」后面，未展开时不在 DOM 里，因此查找过程中要展开。
func findCommentElement(ctx context.Context, page *rod.Page, commentID, userID string) (*rod.Element, error) {
	logrus.Infof("开始查找评论 - commentID: %s, userID: %s", commentID, userID)

	scrollToCommentsArea(page)
	humanize.Delay(ctx, humanize.BetweenScroll)

	lastCommentCount := 0
	stagnantChecks := 0
	expandRounds := 0
	deadline := time.Now().Add(maxSearchDuration)

	// attempt 只数下滚，展开不计入，由 deadline 收口。
	for attempt := 0; attempt < maxSearchScrolls; {
		if time.Now().After(deadline) {
			logrus.Warnf("查找超过 %s，停止", maxSearchDuration)
			return nil, fmt.Errorf("评论区过大，%s 内未找到目标评论 (commentID: %s, userID: %s)",
				maxSearchDuration, commentID, userID)
		}

		// 1. 先查已渲染的评论
		if el := lookupComment(page, commentID, userID); el != nil {
			logrus.Infof("✓ 找到目标评论（下滚 %d 次）", attempt)
			return el, nil
		}

		// 2. 展开视野里的楼中楼后再查一次
		if expandRounds < maxExpandRounds {
			if expanded := expandNearbyReplies(ctx, page); expanded > 0 {
				expandRounds++
				humanize.Delay(ctx, humanize.Reading)

				if el := lookupComment(page, commentID, userID); el != nil {
					logrus.Infof("✓ 展开楼中楼后找到目标评论（下滚 %d 次）", attempt)
					return el, nil
				}

				continue
			}
		}
		expandRounds = 0
		attempt++

		// 3. 到底则不再加载
		if checkEndContainer(page) {
			logrus.Info("已到达评论底部，未找到目标评论")
			break
		}

		// 4. 评论数停滞说明加载不动了
		currentCount := getCommentCount(page)
		if currentCount != lastCommentCount {
			lastCommentCount = currentCount
			stagnantChecks = 0
		} else {
			stagnantChecks++
			if stagnantChecks >= 10 {
				logrus.Info("评论数量停滞，停止查找")
				break
			}
		}

		// 5. 滚到最后一条评论再继续下滚，触发懒加载
		if currentCount > 0 {
			if elements, err := page.Timeout(2 * time.Second).Elements(".comment-item"); err == nil && len(elements) > 0 {
				if err := elements[len(elements)-1].ScrollIntoView(); err != nil {
					logrus.Debugf("滚动到最后一条评论失败: %v", err)
				}
			}
			humanize.Delay(ctx, humanize.BetweenScroll)
		}

		humanScroll(ctx, page, "normal", false, 1)
		humanize.Delay(ctx, humanize.BetweenScroll)
	}

	return nil, fmt.Errorf("未找到评论 (commentID: %s, userID: %s)", commentID, userID)
}

// lookupComment 在当前已渲染的评论里查找目标，找不到返回 nil。
func lookupComment(page *rod.Page, commentID, userID string) *rod.Element {
	return lookupCommentTarget(commentID, userID,
		func(id string) *rod.Element {
			if el, err := page.Timeout(2 * time.Second).Element(fmt.Sprintf("#comment-%s", id)); err == nil {
				return el
			}
			return nil
		},
		func(id string) *rod.Element {
			// 一级和二级评论都带 .comment-item；.parent-comment 会把整个楼层一起匹配上。
			elements, err := page.Timeout(2 * time.Second).Elements(".comment-item")
			if err != nil {
				return nil
			}
			for _, el := range elements {
				if userEl, err := el.Timeout(500 * time.Millisecond).Element(fmt.Sprintf(`[data-user-id="%s"]`, id)); err == nil && userEl != nil {
					return el
				}
			}
			return nil
		})
}

// lookupCommentTarget 将评论定位策略与页面查询分开。
func lookupCommentTarget(commentID, userID string, findByID, findByUserID func(string) *rod.Element) *rod.Element {
	if commentID != "" {
		// 同一用户可能有多条评论；ID 未命中时留给上层展开、滚动后重查，不能按用户兜底。
		return findByID(commentID)
	}
	if userID != "" {
		return findByUserID(userID)
	}
	return nil
}
