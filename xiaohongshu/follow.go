package xiaohongshu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// FollowResult reports current-account observations, never cross-account acceptance.
type FollowResult struct {
	UserID                string                  `json:"user_id"`
	Following             bool                    `json:"observed_following"`
	Mutual                bool                    `json:"observed_mutual"`
	Changed               bool                    `json:"observed_page_state_changed"`
	VerifiedBy            string                  `json:"observation_source"`
	Status                string                  `json:"status"`
	TargetAccountVerified bool                    `json:"target_account_verified"`
	RetrySafe             bool                    `json:"retry_safe"`
	Notice                string                  `json:"notice"`
	ActorBefore           string                  `json:"actor_user_id_before"`
	ActorAfter            string                  `json:"actor_user_id_after"`
	StoreRelationship     string                  `json:"profile_store_relationship"`
	Requests              []followRequestEvidence `json:"request_evidence"`
}

func observedFollow(userID, state, source string, changed bool, before, after followSnapshot) *FollowResult {
	return &FollowResult{UserID: userID, Following: true, Mutual: state == "mutual", Changed: changed, VerifiedBy: source,
		Status: "current_account_observation", TargetAccountVerified: false, RetrySafe: false,
		Notice:      "仅记录操作账号页面观察，目标账号是否收到关注未核实；不能据此宣称关注已通过跨账号验收，不要自动重试。",
		ActorBefore: before.ActorID, ActorAfter: after.ActorID, StoreRelationship: after.Relationship}
}

// FollowUnconfirmedError is deliberately not retry-safe after a click attempt.
type FollowUnconfirmedError struct {
	UserID string
	Cause  error
}

func (e *FollowUnconfirmedError) Error() string {
	return fmt.Sprintf("submitted_unconfirmed; retry_safe=false; 已尝试关注但未核实持久关系，请先查看目标主页，不要重复提交: %v", e.Cause)
}
func (e *FollowUnconfirmedError) Unwrap() error { return e.Cause }

type followButton struct {
	Index    int    `json:"index"`
	Text     string `json:"text"`
	Disabled bool   `json:"disabled"`
}
type followSnapshot struct {
	TargetID      string         `json:"target_id"`
	IdentityError string         `json:"identity_error"`
	Self          bool           `json:"self"`
	Buttons       []followButton `json:"buttons"`
	ActorID       string         `json:"actor_id"`
	Relationship  string         `json:"relationship"`
}
type followPage interface {
	snapshot(context.Context, string) (followSnapshot, error)
	click(context.Context, string, followButton) error
	verify(context.Context, string) (followSnapshot, error)
}

var followUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidateFollowIdentity(userID, token string) error {
	if !followUserIDPattern.MatchString(userID) {
		return fmt.Errorf("用户ID格式无效")
	}
	if strings.TrimSpace(token) == "" || len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("用户访问令牌无效")
	}
	return nil
}
func followState(text string) string {
	text = strings.Join(strings.Fields(text), "")
	switch text {
	case "关注", "+关注", "＋关注":
		return "unfollowed"
	case "回关", "+回关", "＋回关":
		return "follows_me"
	case "已关注":
		return "following"
	case "互相关注", "相互关注":
		return "mutual"
	}
	return ""
}
func selectFollowButton(s followSnapshot, userID string) (followButton, string, error) {
	if s.IdentityError != "" || s.TargetID != userID {
		return followButton{}, "", fmt.Errorf("无法核实目标用户主页，未执行关注")
	}
	if s.Self {
		return followButton{}, "", fmt.Errorf("不能关注自己的账号")
	}
	var found []followButton
	for _, b := range s.Buttons {
		if followState(b.Text) != "" {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		return followButton{}, "", fmt.Errorf("目标主页关注按钮缺失或不唯一，未执行关注")
	}
	if found[0].Disabled {
		return followButton{}, "", fmt.Errorf("关注按钮不可用")
	}
	return found[0], followState(found[0].Text), nil
}
func performFollow(ctx context.Context, page followPage, userID string, backOnly bool, timeout time.Duration) (*FollowResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := page.snapshot(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !followUserIDPattern.MatchString(before.ActorID) {
		return nil, fmt.Errorf("未能核实执行关注的登录账号，尚未点击")
	}
	button, state, err := selectFollowButton(before, userID)
	if err != nil {
		return nil, err
	}
	if state == "mutual" || (!backOnly && state == "following") {
		return observedFollow(userID, state, "profile_initial_state", false, before, before), nil
	}
	if backOnly && state != "follows_me" {
		return nil, fmt.Errorf("未确认对方已关注当前账号，未执行回关")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := page.click(ctx, userID, button); err != nil {
		return nil, &FollowUnconfirmedError{userID, err}
	}
	// Do not click twice: a second click can reverse a write with a delayed response.
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		after, readErr := page.snapshot(ctx, userID)
		if readErr == nil {
			_, next, selectErr := selectFollowButton(after, userID)
			if selectErr == nil && (next == "mutual" || (!backOnly && next == "following")) {
				// A button may change optimistically before a request succeeds. Reload
				// once without cache and recheck identity before reporting an observation.
				verifyCtx, cancel := context.WithTimeout(ctx, timeout)
				persisted, verifyErr := page.verify(verifyCtx, userID)
				cancel()
				if verifyErr != nil {
					return nil, &FollowUnconfirmedError{userID, verifyErr}
				}
				_, confirmed, identityErr := selectFollowButton(persisted, userID)
				if identityErr != nil {
					return nil, &FollowUnconfirmedError{userID, identityErr}
				}
				if before.ActorID != persisted.ActorID {
					return nil, &FollowUnconfirmedError{userID, fmt.Errorf("刷新前后操作账号变化，无法核实关系")}
				}
				if confirmed != "mutual" && (backOnly || confirmed != "following") {
					return nil, &FollowUnconfirmedError{userID, fmt.Errorf("刷新后未确认关注关系，不能以按钮乐观更新作为成功")}
				}
				return observedFollow(userID, confirmed, "profile_reload", true, before, persisted), nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, &FollowUnconfirmedError{userID, ctx.Err()}
		case <-deadline.C:
			return nil, &FollowUnconfirmedError{userID, fmt.Errorf("点击后目标按钮状态未确认")}
		case <-ticker.C:
		}
	}
}

// Never search all page buttons: recommendation cards can belong to other users.
const followDOMHelpers = `
const unwrap=v=>v&&(v.value??v._value??v._rawValue??v);
const path=location.pathname.match(/^\/user\/profile\/([^/]+)\/?$/);
const user=window.__INITIAL_STATE__&&unwrap(window.__INITIAL_STATE__.user);
const data=user&&unwrap(user.userPageData);
const basic=data&&unwrap(data.basicInfo);
const stateID=String((basic&&(basic.userId||basic.user_id))||(data&&(data.userId||data.user_id))||'');
// Current profile payloads omit basicInfo.userId. The resolved profile store's
// noteQueries still binds each tab to the requested user, including empty tabs.
const queries=user&&unwrap(user.noteQueries);
const queryIDs=Array.isArray(queries)?queries.map(q=>String(unwrap(q)?.userId||unwrap(q)?.user_id||'')):[];
const resolved=user&&unwrap(user.userFetchingStatus)==='resolved'&&data?.result?.success===true&&data.result.code===0;
const storeIdentity=stateID?stateID===target:resolved&&!!basic?.nickname&&!!basic?.redId&&queryIDs.length>0&&queryIDs.every(id=>id===target);
const visible=e=>!!(e.getClientRects().length&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none');
const headers=[...document.querySelectorAll('[data-testid="user-profile-header"], .user-page .user-info, .user-page .user-info-container, .user-page .user > .buttons, .user-profile > .user-info')].filter(visible);
const unique=headers.filter(e=>!headers.some(other=>other!==e&&other.contains(e)));
const header=unique.length===1?unique[0]:null;
const identityError=!path||decodeURIComponent(path[1])!==target||!storeIdentity||!header?'目标主页未就绪或身份不符':'';
const all=header?[...header.querySelectorAll('button,[role="button"],.follow-btn,.follow-button')].filter(visible):[];
const candidates=all.filter(e=>!all.some(other=>other!==e&&other.contains(e)));
const text=e=>e.innerText.trim();
const disabled=e=>!!e.disabled||e.getAttribute('aria-disabled')==='true';
`
const followSnapshotJS = `(target)=>{` + followDOMHelpers + `
const actorLinks=[...document.querySelectorAll('.side-bar-component.user a[href*="/user/profile/"],.bottom-channel .user a[href*="/user/profile/"]')].map(e=>new URL(e.href,location.href).pathname.match(/^\/user\/profile\/([^/]+)\/?$/)?.[1]).filter(Boolean);
const actorIDs=[...new Set(actorLinks)];
return {target_id:identityError?'':target,identity_error:identityError,actor_id:actorIDs.length===1?actorIDs[0]:'',relationship:String(data?.extraInfo?.fstatus||''),self:!!header&&/编辑资料|编辑个人资料/.test(header.innerText),buttons:candidates.map((e,index)=>({index,text:text(e),disabled:disabled(e)}))};
}`
const followClickJS = `(target,index,expected)=>{` + followDOMHelpers + `
if(identityError||!header||/编辑资料|编辑个人资料/.test(header.innerText))return false;
const button=candidates[index];
const known=candidates.filter(e=>/^(关注|\+\s*关注|＋\s*关注|回关|\+\s*回关|＋\s*回关|已关注|互相关注|相互关注)$/.test(text(e)));
if(known.length!==1||!button||text(button)!==expected||disabled(button))return false;
return button;
}`

type rodFollowPage struct{ page *rod.Page }

func (p rodFollowPage) verify(ctx context.Context, userID string) (followSnapshot, error) {
	page := p.page.Context(ctx)
	if err := rod.Try(func() { page.WaitRequestIdle(300*time.Millisecond, nil, nil, nil)() }); err != nil {
		return followSnapshot{}, fmt.Errorf("等待关注请求结束失败: %w", err)
	}
	if err := (proto.PageReload{IgnoreCache: true}).Call(page); err != nil {
		return followSnapshot{}, fmt.Errorf("刷新验证关注关系失败: %w", err)
	}
	if err := page.WaitLoad(); err != nil {
		return followSnapshot{}, fmt.Errorf("刷新目标主页未完成: %w", err)
	}
	if err := page.Wait(rod.Eval(`(target)=>{`+followDOMHelpers+`return !identityError&&candidates.length>0;}`, userID)); err != nil {
		return followSnapshot{}, fmt.Errorf("刷新后目标身份或关系未就绪: %w", err)
	}
	return (rodFollowPage{page}).snapshot(ctx, userID)
}

func (p rodFollowPage) snapshot(ctx context.Context, userID string) (followSnapshot, error) {
	value, err := p.page.Context(ctx).Eval(followSnapshotJS, userID)
	if err != nil {
		return followSnapshot{}, fmt.Errorf("读取关注状态失败: %w", err)
	}
	var result followSnapshot
	if err := value.Value.Unmarshal(&result); err != nil {
		return result, err
	}
	return result, nil
}
func (p rodFollowPage) click(ctx context.Context, userID string, b followButton) error {
	idleCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waitIdle := p.page.Context(idleCtx).WaitRequestIdle(300*time.Millisecond, nil, nil, nil)
	page := p.page.Context(ctx)
	selected, err := page.Evaluate(rod.Eval(followClickJS, userID, b.Index, b.Text).ByObject())
	if err != nil {
		return fmt.Errorf("点击关注失败: %w", err)
	}
	if selected.Subtype != proto.RuntimeRemoteObjectSubtypeNode {
		return fmt.Errorf("目标按钮已变化或身份不符，未执行关注")
	}
	button, err := page.ElementFromObject(selected)
	if err != nil {
		return err
	}
	// Use browser input so the website receives a trusted pointer event. DOM
	// element.click() can be ignored even when the selector and state are correct.
	if err := button.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("浏览器点击关注未确认完成: %w", err)
	}
	if err := rod.Try(waitIdle); err != nil {
		return fmt.Errorf("等待关注请求完成失败: %w", err)
	}
	return nil
}

// FollowUser only follows; this API intentionally cannot unfollow an account.
func FollowUser(ctx context.Context, page *rod.Page, userID, token string, backOnly bool) (*FollowResult, error) {
	if err := ValidateFollowIdentity(userID, token); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	page = page.Context(ctx)
	if err := page.Navigate(makeUserProfileURL(userID, url.QueryEscape(token), TabNotes)); err != nil {
		return nil, fmt.Errorf("打开目标主页失败: %w", err)
	}
	if err := page.Wait(rod.Eval(`(target)=>{`+followDOMHelpers+`return !identityError&&candidates.length>0;}`, userID)); err != nil {
		return nil, fmt.Errorf("目标主页关注区域未就绪: %w", err)
	}
	before, err := (rodFollowPage{page}).snapshot(ctx, userID)
	if err != nil || before.ActorID == "" {
		return nil, fmt.Errorf("未能核实执行关注的登录账号，尚未点击")
	}
	evidence, stop := watchFollowEvidence(page, userID)
	defer stop()
	result, err := performFollow(ctx, rodFollowPage{page}, userID, backOnly, 8*time.Second)
	if result != nil {
		result.Requests = evidence()
	}
	if err != nil {
		diagnostic, _ := json.Marshal(evidence())
		return nil, fmt.Errorf("actor_user_id=%s request_evidence=%s: %w", before.ActorID, diagnostic, err)
	}
	return result, err
}
