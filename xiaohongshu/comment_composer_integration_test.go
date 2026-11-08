//go:build integration

package xiaohongshu

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
	"testing"
	"time"
)

func TestFeedComposerRequiresActivationEvenWhenEditorExists(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	p := b.MustPage("about:blank")
	defer p.MustClose()
	p.MustSetDocumentContent(`<script>window.sent=0;window.opened=0</script><div class="input-box"><div class="content-edit"><p class="content-input" contenteditable="true"></p><div class="not-active" onclick="window.opened++;document.querySelector(&quot;.bottom&quot;).style.opacity=1;this.style.display=&quot;none&quot;"><span>说点什么...</span></div></div><div class="bottom" style="opacity:0"><button class="submit" onclick="window.sent++">发送</button></div></div>`)
	editor := p.MustElement(feedComposerSelector)
	require.NoError(t, humanize.Type(context.Background(), editor, "草稿"))
	_, err := readyFeedSubmit(p)
	require.Error(t, err)
	require.Equal(t, 0, p.MustEval(`()=>window.sent`).Int())
	require.NoError(t, activateFeedComposer(context.Background(), p))
	button, err := readyFeedSubmit(p)
	require.NoError(t, err)
	require.NoError(t, humanize.Click(button.Timeout(3*time.Second)))
	require.Equal(t, 1, p.MustEval(`()=>window.sent`).Int())
	require.NoError(t, activateFeedComposer(context.Background(), p))
	require.Equal(t, 1, p.MustEval(`()=>window.opened`).Int())
}

func TestFeedComposerRejectsAmbiguousActivationAndSubmit(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	p := b.MustPage("about:blank")
	defer p.MustClose()
	p.MustSetDocumentContent(`<div class="input-box"><div class="content-edit"><div class="not-active"><span>说点什么</span><span>另一个</span></div></div></div>`)
	require.ErrorContains(t, activateFeedComposer(context.Background(), p), "多个")
	p.MustSetDocumentContent(`<div class="bottom"><button class="submit">发送</button><button class="submit">发送</button></div>`)
	_, err := readyFeedSubmit(p)
	require.Error(t, err)
	p.MustSetDocumentContent(`<div class="bottom"><button class="submit" disabled>发送</button></div>`)
	_, err = readyFeedSubmit(p)
	require.ErrorContains(t, err, "不可用")
}
