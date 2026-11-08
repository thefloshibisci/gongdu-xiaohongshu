package xiaohongshu

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommentSubmissionPhasesPreventUnsafeRetry(t *testing.T) {
	before := commentNotSubmitted(errors.New("editor absent"))
	require.Contains(t, before.Error(), "[not_submitted]")
	require.NotContains(t, before.Error(), "submitted_unconfirmed")
	after := commentUnconfirmed(errors.New("rendering delayed"))
	require.Contains(t, after.Error(), "submitted_unconfirmed")
	require.Contains(t, after.Error(), "retry_safe=false")
	require.Contains(t, after.Error(), "不要自动重试")
	require.Contains(t, after.Error(), "不要把回复改发为顶层评论")
	var phase *CommentSubmissionError
	require.ErrorAs(t, after, &phase)
	require.Equal(t, "submitted_unconfirmed", phase.Phase)
}

func TestReplyPlaceholderMustIdentifyExactTarget(t *testing.T) {
	for _, placeholder := range []string{"回复 小红薯", "回复 @小红薯：", "Reply to 小红薯:"} {
		require.True(t, replyPlaceholderMatches(placeholder, "小红薯"), placeholder)
	}
	for _, placeholder := range []string{"说点什么", "回复 小红薯的朋友", "回复 另一个人", ""} {
		require.False(t, replyPlaceholderMatches(placeholder, "小红薯"), placeholder)
	}
	require.False(t, replyPlaceholderMatches("回复 小红薯", ""))
}

func TestNewCommentCannotReuseExistingTextOrUnrelatedThread(t *testing.T) {
	before := []renderedComment{{ID: "old", Text: "同一内容", Thread: "thread"}}
	require.False(t, newlyRenderedComment(before, before, "同一内容", "thread"))
	require.False(t, newlyRenderedComment(before, []renderedComment{{ID: "new", Text: "同一内容", Thread: "other"}}, "同一内容", "thread"))
	require.False(t, newlyRenderedComment(before, []renderedComment{{ID: "new", Text: "包含同一内容的其他文字", Thread: "thread"}}, "同一内容", "thread"))
	require.False(t, newlyRenderedComment(before, []renderedComment{{ID: "", Text: "同一内容", Thread: "thread"}}, "同一内容", "thread"))
	require.True(t, newlyRenderedComment(before, []renderedComment{{ID: "new", Text: "同一内容", Thread: "thread"}}, "同一内容", "thread"))
}

func TestCommentRequestObserverOnlyMatchesSameSend(t *testing.T) {
	endpoint := "https://edith.xiaohongshu.com/api/sns/web/v1/comment/post"
	body := `{"note_id":"feed","content":"你好","target_comment_id":"target"}`
	require.True(t, commentRequestMatches(endpoint, "POST", body, "feed", "target", "你好"))
	require.False(t, commentRequestMatches(endpoint, "POST", body, "feed", "", "你好"))
	require.False(t, commentRequestMatches(endpoint, "POST", body, "other", "target", "你好"))
	require.False(t, commentRequestMatches(endpoint, "POST", body, "feed", "target", "另一条"))
	require.False(t, commentRequestMatches(endpoint, "GET", body, "feed", "target", "你好"))
	require.False(t, commentRequestMatches("https://xiaohongshu.com.evil.test/api/sns/web/v1/comment/post", "POST", body, "feed", "target", "你好"))
}

func TestCommentResponseRequiresSuccessAndNewID(t *testing.T) {
	for _, body := range []string{`{"success":true,"code":0,"data":{"comment":{"id":"new"}}}`, `{"success":true,"code":0,"data":{"comment_id":"new"}}`} {
		require.NoError(t, parseCommentResponse([]byte(body)).err)
	}
	for _, body := range []string{`{"success":true}`, `{"success":true,"code":0,"data":{}}`, `{"success":false,"code":0,"data":{"id":"new"}}`, `{"success":true,"code":-1,"data":{"id":"new"}}`, `not JSON`} {
		require.Error(t, parseCommentResponse([]byte(body)).err, body)
	}
}

func TestCommentResponseTimeoutRemainsUnconfirmed(t *testing.T) {
	err := waitCommentResponse(context.Background(), make(chan commentResponse), time.Millisecond)
	require.Contains(t, err.Error(), "submitted_unconfirmed")
	require.Contains(t, err.Error(), "retry_safe=false")
}

func TestPersistedCommentRequiresAcknowledgedIDContentAndThread(t *testing.T) {
	before := []renderedComment{{ID: "comment-old", Text: "你好", Thread: "root"}}
	after := []renderedComment{{ID: "comment-new", Text: "你好", Thread: "comment-root"}}
	require.True(t, persistedCommentMatches(before, after, "你好", "root", "new"))
	require.False(t, persistedCommentMatches(before, before, "你好", "root", "old"))
	require.False(t, persistedCommentMatches(before, after, "你好", "root", "other"))
	require.False(t, persistedCommentMatches(before, after, "你好", "other", "new"))
	require.False(t, persistedCommentMatches(before, after, "不同内容", "root", "new"))
	require.False(t, persistedCommentMatches(before, after, "你好", "root", ""))
}
