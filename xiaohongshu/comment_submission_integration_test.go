//go:build integration

package xiaohongshu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

func TestCommentComposerLocalVariantsAndExactTarget(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	for _, markup := range []string{
		`<textarea class="comment-input" placeholder="回复 小红薯"></textarea>`,
		`<div class="comment-input" contenteditable="true" data-placeholder="回复 小红薯"></div>`,
		`<div class="content-edit"><p contenteditable="true" data-placeholder="回复 小红薯"></p></div>`,
	} {
		page := b.MustPage("about:blank")
		page.MustSetDocumentContent(`<div id="target">` + markup + `</div><textarea class="comment-input" placeholder="回复 另一个人"></textarea>`)
		item := page.MustElement("#target")
		input, err := visibleCommentInput(context.Background(), func() (rod.Elements, error) { return item.Elements(notificationComposerSelector) }, time.Second)
		require.NoError(t, err)
		require.NoError(t, verifyReplyTarget(input, "小红薯"))
		require.Error(t, verifyReplyTarget(input, "小红薯的朋友"))
		page.MustClose()
	}
}

func TestCommentAckAndOptimisticDOMRequireReloadedPersistence(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	for _, persisted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ack_only_unconfirmed", true: "persisted_current_account"}[persisted], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if persisted {
					w.Write([]byte(`<div class="comments-container"><div id="comment-new" class="comment-item"><div class="content">你好</div></div></div>`))
				} else {
					w.Write([]byte(`<div class="comments-container"></div>`))
				}
			}))
			defer srv.Close()
			page := b.MustPage(srv.URL).MustWaitLoad()
			defer page.MustClose()
			page.MustEval(`()=>{document.querySelector('.comments-container').innerHTML='<div class="comment-item" id="comment-new"><div class="content">你好</div></div>';}`)
			responses := make(chan commentResponse, 1)
			responses <- commentResponse{id: "new"}
			err := waitNewComment(context.Background(), page, nil, "你好", "", responses, 600*time.Millisecond)
			if persisted {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "submitted_unconfirmed")
				require.ErrorContains(t, err, "retry_safe=false")
			}
		})
	}
}

func TestCommentPersistedVerificationExpandsCollapsedReplies(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	p := b.MustPage("about:blank")
	defer p.MustClose()
	p.MustSetDocumentContent(`<div class="comments-container"><div class="parent-comment"><div class="comment-item" id="comment-root"><div class="content">原评论</div></div><div class="replies"></div><button class="show-more" onclick="this.previousElementSibling.innerHTML='<div class=&quot;comment-item&quot; id=&quot;comment-new&quot;><div class=&quot;content&quot;>你好</div></div>';this.remove()">展开 1 条回复</button></div></div>`)
	require.NoError(t, waitPersistedComment(context.Background(), p, nil, "你好", "root", "new", 3*time.Second))
}

func TestCommentComposerLocalRejectsHiddenAndAmbiguousInputs(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	page := b.MustPage("about:blank")
	defer page.MustClose()
	page.MustSetDocumentContent(`<div class="input-box"><textarea class="comment-input" style="display:none"></textarea><textarea class="comment-input" placeholder="说点什么"></textarea></div>`)
	_, err := visibleCommentInput(context.Background(), func() (rod.Elements, error) { return page.Elements(feedComposerSelector) }, time.Second)
	require.NoError(t, err)
	page.MustSetDocumentContent(`<div class="input-box"><textarea class="comment-input"></textarea><textarea class="comment-input"></textarea></div>`)
	_, err = visibleCommentInput(context.Background(), func() (rod.Elements, error) { return page.Elements(feedComposerSelector) }, time.Second)
	require.ErrorContains(t, err, "多个")
}

func TestCommentComposerNeverSelectsHeaderSearch(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	p := b.MustPage("about:blank")
	defer p.MustClose()
	search := `<header class="input-box"><div class="textarea-wrapper"><textarea name="aiSearchTextarea" style="opacity:0.00001"></textarea><textarea id="search-input-in-feeds" name="aiSearchTextarea" placeholder="小手机接入mcp教程">搜索内容保持不变</textarea></div></header>`
	for _, editor := range []string{
		`<div class="content-edit"><p id="actual-comment" class="content-input" contenteditable="true"></p></div>`,
		`<div class="content-edit"><textarea id="actual-comment"></textarea></div>`,
		`<textarea id="actual-comment" class="comment-input"></textarea>`,
	} {
		p.MustSetDocumentContent(search + `<section class="input-box">` + editor + `</section>`)
		input, err := visibleCommentInput(context.Background(), func() (rod.Elements, error) { return p.Elements(feedComposerSelector) }, time.Second)
		require.NoError(t, err)
		id, err := input.Attribute("id")
		require.NoError(t, err)
		require.Equal(t, "actual-comment", *id)
		input.MustInput("评论正文")
		require.Equal(t, "搜索内容保持不变", p.MustElement("#search-input-in-feeds").MustProperty("value").Str())
	}
	p.MustSetDocumentContent(search)
	_, err := visibleCommentInput(context.Background(), func() (rod.Elements, error) { return p.Elements(feedComposerSelector) }, 150*time.Millisecond)
	require.ErrorContains(t, err, "输入框未出现")
}

func TestCommentRenderedLocalChecksNewIdentityAndThread(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	page := b.MustPage("about:blank")
	defer page.MustClose()
	page.MustSetDocumentContent(`<div class="comments-container"><div class="parent-comment"><div class="comment-item" id="comment-root"><span class="user-name">甲</span><div class="content">同一内容</div></div><div class="replies"></div></div></div>`)
	before, err := renderedComments(page)
	require.NoError(t, err)
	require.False(t, newlyRenderedComment(before, before, "同一内容", "comment-root"))
	page.MustEval(`() => {document.querySelector('.replies').innerHTML='<div class="comment-item" id="comment-new"><div class="content">同一内容</div></div>';}`)
	after, err := renderedComments(page)
	require.NoError(t, err)
	require.True(t, newlyRenderedComment(before, after, "同一内容", "comment-root"))
	require.False(t, newlyRenderedComment(before, after, "同一内容", "comment-other"))
}

// All URLs are locally intercepted: no account cookies or real comment requests.
func TestCommentResponseObserverLocalMatchesBrowserSend(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	page := b.MustPage()
	defer page.MustClose()
	router := page.HijackRequests()
	router.MustAdd("*", func(h *rod.Hijack) {
		h.Response.SetHeader("Content-Type", "application/json")
		if h.Request.URL().Path == "/api/sns/web/v1/comment/post" {
			h.Response.SetBody(`{"success":true,"code":0,"data":{"comment":{"id":"new"}}}`)
		} else {
			h.Response.SetHeader("Content-Type", "text/html")
			h.Response.SetBody("<html><body>local fixture</body></html>")
		}
	})
	go router.Run()
	defer router.Stop()
	page.MustNavigate("https://www.xiaohongshu.com/fixture-comment").MustWaitLoad()
	responses, stop := watchCommentResponse(page, "feed", "target", "你好")
	defer stop()
	page.MustEval(`() => {fetch('/api/sns/web/v1/comment/post', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({note_id:'feed',target_comment_id:'target',content:'你好'})});}`)
	require.NoError(t, waitCommentResponse(context.Background(), responses, 3*time.Second))
}
