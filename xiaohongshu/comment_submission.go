package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

// CommentSubmissionError distinguishes a missing composer from an ambiguous send.
// Once clicking send has started, no caller may infer that retrying is harmless.
type CommentSubmissionError struct {
	Phase string
	Cause error
}

func (e *CommentSubmissionError) Error() string {
	if e.Phase == "submitted_unconfirmed" {
		return fmt.Sprintf("[submitted_unconfirmed; retry_safe=false] 已尝试提交，但未确认发布结果：%v。内容可能已经发出，请先人工查看原评论；不要自动重试，也不要把回复改发为顶层评论", e.Cause)
	}
	return fmt.Sprintf("[not_submitted] 尚未点击发送：%v。不要把指定评论的回复改发为顶层评论", e.Cause)
}

func (e *CommentSubmissionError) Unwrap() error { return e.Cause }

func commentNotSubmitted(err error) error {
	return &CommentSubmissionError{Phase: "not_submitted", Cause: err}
}

func commentUnconfirmed(err error) error {
	return &CommentSubmissionError{Phase: "submitted_unconfirmed", Cause: err}
}

// The header search UI also uses .input-box and a textarea. Only select
// editors inside .content-edit or explicitly marked as comment inputs.
const feedComposerSelector = ".input-box .content-edit p.content-input, .input-box .content-edit [contenteditable='true'], .input-box .content-edit textarea, .input-box textarea.comment-input"
const notificationComposerSelector = "textarea.comment-input, .comment-input[contenteditable='true'], .content-edit [contenteditable='true']"

// Poll multiple known composer variants, but never choose a hidden or ambiguous editor.
func visibleCommentInput(ctx context.Context, elements func() (rod.Elements, error), timeout time.Duration) (*rod.Element, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		all, err := elements()
		if err != nil {
			return nil, err
		}
		var visible []*rod.Element
		for _, element := range all {
			if humanize.Visible(element) {
				visible = append(visible, element)
			}
		}
		if len(visible) == 1 {
			return visible[0], nil
		}
		if len(visible) > 1 {
			return nil, fmt.Errorf("出现多个可见评论输入框，无法安全确定目标")
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("回复输入框未出现（已检查 textarea 和 contenteditable）")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func commentInputPlaceholder(input *rod.Element) (string, error) {
	result, err := input.Eval(`() => {
	  const values = [this.getAttribute('placeholder'), this.getAttribute('data-placeholder'), this.getAttribute('aria-label')];
	  const parent = this.closest('.content-edit');
	  if (parent) values.push(parent.getAttribute('data-placeholder'), parent.getAttribute('placeholder'));
	  return values.filter(v => v && v.trim())[0] || '';
	}`)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Value.Str()), nil
}

func replyPlaceholderMatches(placeholder, nickname string) bool {
	placeholder = strings.TrimSpace(placeholder)
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		return false
	}
	for _, prefix := range []string{"回复", "Reply to", "reply to"} {
		if strings.HasPrefix(placeholder, prefix) {
			target := strings.TrimSpace(strings.TrimPrefix(placeholder, prefix))
			target = strings.TrimPrefix(target, "@")
			target = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(target, "："), ":"))
			return target == nickname
		}
	}
	return false
}

func feedReplyNickname(comment *rod.Element) (string, error) {
	value, err := comment.Eval(`() => {
	  const names = Array.from(this.querySelectorAll('.author .name, .author-wrapper .name, a.name, .user-name, .nickname, a[data-user-id]'));
	  const own = names.find(n => n.closest('.comment-item') === this);
	  return own ? (own.innerText || '').trim() : '';
	}`)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value.Value.Str()) == "" {
		return "", fmt.Errorf("无法读取目标评论作者，不能确认回复对象")
	}
	return value.Value.Str(), nil
}

type renderedComment struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Thread string `json:"thread"`
}

// Read exact comment bodies rather than the entire comments container's substring.
// This prevents a pre-existing comment, a nickname, or a nested unrelated text from
// being mistaken for a successful submission.
func renderedComments(page *rod.Page) ([]renderedComment, error) {
	result, err := page.Eval(`() => JSON.stringify(Array.from(document.querySelectorAll('.comments-container .comment-item')).map(item => {
	  const own = selector => Array.from(item.querySelectorAll(selector)).find(n => n.closest('.comment-item') === item);
	  const content = own('.content, .comment-content');
	  const thread = item.closest('.parent-comment');
	  const parent = thread && (thread.matches('.comment-item') ? thread : thread.querySelector('.comment-item'));
	  return {id: item.id || item.getAttribute('data-comment-id') || '', text: content ? content.innerText : '', thread: parent ? (parent.id || parent.getAttribute('data-comment-id') || '') : (item.id || item.getAttribute('data-comment-id') || '')};
	}))`)
	if err != nil {
		return nil, err
	}
	var comments []renderedComment
	if err := json.Unmarshal([]byte(result.Value.Str()), &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

func normalizedCommentText(text string) string { return strings.Join(strings.Fields(text), " ") }

func newlyRenderedComment(before, after []renderedComment, content, thread string) bool {
	known := make(map[string]bool, len(before))
	for _, comment := range before {
		if comment.ID != "" {
			known[comment.ID] = true
		}
	}
	for _, comment := range after {
		if comment.ID == "" || known[comment.ID] {
			continue
		}
		if normalizedCommentText(comment.Text) != normalizedCommentText(content) {
			continue
		}
		if thread != "" && comment.Thread != thread {
			continue
		}
		return true
	}
	return false
}

// An API ACK or optimistic DOM update alone is never a publication receipt.
func waitNewComment(ctx context.Context, page *rod.Page, before []renderedComment, content, thread string, responses <-chan commentResponse, timeout time.Duration) error {
	id, err := waitCommentAcknowledgement(ctx, responses, timeout)
	if err != nil {
		return err
	}
	if err := (proto.PageReload{IgnoreCache: true}).Call(page); err != nil {
		return commentUnconfirmed(err)
	}
	if err := page.WaitLoad(); err != nil {
		return commentUnconfirmed(err)
	}
	return waitPersistedComment(ctx, page, before, content, thread, id, timeout)
}
func persistedCommentMatches(before, after []renderedComment, content, thread, id string) bool {
	normalize := func(v string) string { return strings.TrimPrefix(v, "comment-") }
	if id == "" {
		return false
	}
	for _, c := range before {
		if normalize(c.ID) == normalize(id) {
			return false
		}
	}
	for _, c := range after {
		if normalize(c.ID) == normalize(id) && normalizedCommentText(c.Text) == normalizedCommentText(content) && (thread == "" || normalize(c.Thread) == normalize(thread)) {
			return true
		}
	}
	return false
}
func waitPersistedComment(ctx context.Context, page *rod.Page, before []renderedComment, content, thread, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	page = page.Context(ctx)
	deadline := time.Now().Add(timeout)
	expandRounds := 0
	for {
		if err := ctx.Err(); err != nil {
			return commentUnconfirmed(err)
		}
		after, err := renderedComments(page)
		if err == nil && persistedCommentMatches(before, after, content, thread, id) {
			return nil
		}
		if expandRounds < 2 {
			// Reload collapses reply threads. Expand only read-only reply controls;
			// never press reply/send or infer success from comment counts.
			revealCommentsForVerification(page, thread)
			expandRounds++
		}
		if time.Now().After(deadline) {
			return commentUnconfirmed(fmt.Errorf("接口已接收，但刷新评论区后没有看到对应ID和内容；不保证公开可见"))
		}
		select {
		case <-ctx.Done():
			return commentUnconfirmed(ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func revealCommentsForVerification(page *rod.Page, thread string) {
	_, _ = page.Eval(`(thread)=>{const root=document.querySelector('.comments-container');if(!root)return;const target=thread?[...root.querySelectorAll('.comment-item')].find(e=>(e.id||e.getAttribute('data-comment-id')||'').replace(/^comment-/,'')===thread.replace(/^comment-/,'')):root;(target||root).scrollIntoView({block:'nearest'});}`, thread)
	buttons, err := page.Elements(".comments-container .show-more")
	if err != nil {
		return
	}
	clicked := 0
	for _, button := range buttons {
		if clicked >= 3 {
			break
		}
		text, err := button.Text()
		if err != nil || !isExpandRepliesButton(text) {
			continue
		}
		if thread != "" {
			v, err := button.Eval(`(thread)=>{const p=this.closest('.parent-comment');const c=p&&(p.matches('.comment-item')?p:p.querySelector('.comment-item'));return !!c&&(c.id||c.getAttribute('data-comment-id')||'').replace(/^comment-/,'')===thread.replace(/^comment-/,'');}`, thread)
			if err != nil || !v.Value.Bool() {
				continue
			}
		}
		if err := button.ScrollIntoView(); err == nil {
			if err := button.Click(proto.InputMouseButtonLeft, 1); err == nil {
				clicked++
			}
		}
	}
}
