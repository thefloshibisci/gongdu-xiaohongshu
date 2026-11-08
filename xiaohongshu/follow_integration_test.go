//go:build integration

package xiaohongshu

import (
	"context"
	"fmt"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// All pages are localhost fixtures in a fresh browser with no account cookies.
func newFollowFixtureBrowser(t *testing.T) *rod.Browser {
	t.Helper()
	bin := os.Getenv("XHS_TEST_BROWSER")
	if bin == "" {
		var ok bool
		bin, ok = launcher.LookPath()
		if !ok {
			t.Skip("本地浏览器缺失")
		}
	}
	l := launcher.New().Bin(bin).Headless(true).Leakless(false)
	control, err := l.Launch()
	require.NoError(t, err)
	b := rod.New().ControlURL(control).NoDefaultDevice()
	require.NoError(t, b.Connect())
	t.Cleanup(func() { _ = b.Close(); l.Cleanup() })
	return b
}
func TestFollowLocalDOM(t *testing.T) {
	browser := newFollowFixtureBrowser(t)
	for _, tc := range []struct {
		name, body, stateID string
		back, wantErr       bool
		clicks              int
	}{
		{"trusted_browser_input", `<div class="user-page"><div class="user-info"><button onclick="if(!event.isTrusted)return;window.clicks++;this.innerText='互相关注'">回关</button></div></div>`, "target", true, false, 1},
		{"follow_with_recommendation", `<div class="user-page"><div class="user-info"><button onclick="window.clicks++;this.innerText='已关注'">关注</button></div></div><aside><button onclick="window.wrong++">关注</button></aside>`, "target", false, false, 1},
		{"back", `<div class="user-page"><div class="user-info"><button onclick="window.clicks++;this.innerText='互相关注'">回关</button></div></div>`, "target", true, false, 1},
		{"mutual", `<div class="user-page"><div class="user-info"><button onclick="window.wrong++">互相关注</button></div></div>`, "target", true, false, 0},
		{"recommendation_only", `<aside><button onclick="window.wrong++">关注</button></aside>`, "target", false, true, 0},
		{"wrong_state_user", `<div class="user-page"><div class="user-info"><button onclick="window.wrong++">关注</button></div></div>`, "other", false, true, 0},
		{"missing_state_user", `<div class="user-page"><div class="user-info"><button onclick="window.wrong++">关注</button></div></div>`, "", false, true, 0},
		{"optimistic_rollback", `<div class="user-page"><div class="user-info"><button onclick="window.clicks++;this.innerText='已关注'">关注</button></div></div>`, "target", false, true, 1},
		{"no_effect", `<div class="user-page"><div class="user-info"><button onclick="window.clicks++">关注</button></div></div>`, "target", false, true, 1},
		{"ambiguous", `<div class="user-page"><div class="user-info"><button onclick="window.wrong++">关注</button><button onclick="window.wrong++">回关</button></div></div>`, "target", false, true, 0},
		{"no_follower", `<div class="user-page"><div class="user-info"><button onclick="window.wrong++">关注</button></div></div>`, "target", true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var persisted atomic.Bool
			var attempts, wrong atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					switch r.URL.Path {
					case "/follow":
						persisted.Store(true)
					case "/attempt":
						attempts.Add(1)
					case "/wrong":
						wrong.Add(1)
					}
					w.WriteHeader(204)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				body := strings.ReplaceAll(tc.body, "window.clicks++", "fetch('/attempt',{method:'POST'});window.clicks++")
				body = strings.ReplaceAll(body, "window.wrong++", "fetch('/wrong',{method:'POST'});window.wrong++")
				if !tc.wantErr && tc.clicks == 1 {
					body = strings.ReplaceAll(body, "window.clicks++;", "window.clicks++;fetch('/follow',{method:'POST'});")
				}
				if persisted.Load() {
					body = strings.ReplaceAll(body, ">关注</button>", ">已关注</button>")
					body = strings.ReplaceAll(body, ">回关</button>", ">互相关注</button>")
				}
				fmt.Fprintf(w, `<!doctype html><div class="side-bar-component user"><a href="/user/profile/fixture-actor">我</a></div><script>window.clicks=0;window.wrong=0;window.__INITIAL_STATE__={user:{userPageData:{basicInfo:{userId:%q}}}};</script>%s`, tc.stateID, body)
			}))
			defer srv.Close()
			page := browser.MustPage(srv.URL + "/user/profile/target")
			defer page.Close()
			page.MustWaitLoad()
			result, err := performFollow(context.Background(), rodFollowPage{page}, "target", tc.back, 3*time.Second)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.True(t, result.Following)
			}
			require.Equal(t, int32(tc.clicks), attempts.Load())
			require.Zero(t, wrong.Load())
		})
	}
}

func TestFollowLocalCurrentProfileStoreIdentity(t *testing.T) {
	browser := newFollowFixtureBrowser(t)
	for _, tc := range []struct {
		name, queryID, status, explicit string
		valid                           bool
	}{
		{"current_profile_without_basic_user_id", "target", "resolved", "", true},
		{"loading_profile", "target", "pending", "", false},
		{"stale_profile_query", "other", "resolved", "", false},
		{"conflicting_explicit_identity", "target", "resolved", "other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintf(w, `<script>window.__INITIAL_STATE__={user:{userFetchingStatus:{value:%q},noteQueries:{value:[{userId:%q}]},userPageData:{value:{result:{success:true,code:0},basicInfo:{nickname:'momo',redId:'12345',userId:%q}}}}};</script><div class="ai-mode user-page"><div class="user"><div class="user-info"><div class="xhs-user-follow-area"><button class="follow-button">回关</button></div></div></div></div><aside><button>关注</button></aside>`, tc.status, tc.queryID, tc.explicit)
			}))
			defer srv.Close()
			page := browser.MustPage(srv.URL + "/user/profile/target").MustWaitLoad()
			defer page.MustClose()
			s, err := (rodFollowPage{page}).snapshot(context.Background(), "target")
			require.NoError(t, err)
			_, state, err := selectFollowButton(s, "target")
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, "follows_me", state)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// Intercept all traffic locally; no cookies or platform requests are involved.
func TestFollowEvidenceCapturesActualBrowserResponse(t *testing.T) {
	b := newFollowFixtureBrowser(t)
	page := b.MustPage()
	defer page.MustClose()
	router := page.HijackRequests()
	router.MustAdd("*", func(h *rod.Hijack) {
		h.Response.SetHeader("Content-Type", "application/json")
		if h.Request.URL().Path == "/api/sns/web/v1/user/follow" {
			h.Response.SetBody(`{"success":false,"code":-123,"message":"fixture-only"}`)
		} else {
			h.Response.SetHeader("Content-Type", "text/html")
			h.Response.SetBody("<html><body>local fixture</body></html>")
		}
	})
	go router.Run()
	defer router.Stop()
	page.MustNavigate("https://www.xiaohongshu.com/fixture-follow").MustWaitLoad()
	evidence, stop := watchFollowEvidence(page, "target")
	defer stop()
	page.MustEval(`() => {fetch('/api/sns/web/v1/user/follow', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({target_user_id:'target'})});}`)
	require.Eventually(t, func() bool { rows := evidence(); return len(rows) == 1 && rows[0].Complete }, 3*time.Second, 20*time.Millisecond)
	rows := evidence()
	require.Equal(t, 200, rows[0].HTTPStatus)
	require.Equal(t, -123, *rows[0].Code)
	require.False(t, *rows[0].Success)
	require.True(t, rows[0].TargetMatched)
}
