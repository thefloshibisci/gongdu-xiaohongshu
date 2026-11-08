package xiaohongshu

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type fakeFollowPage struct {
	before, after followSnapshot
	clicks        int
	clickErr      error
	readErr       error
	verifyErr     error
	persisted     *followSnapshot
	onClick       func()
}

func (f *fakeFollowPage) snapshot(context.Context, string) (followSnapshot, error) {
	if f.clicks > 0 {
		return f.after, f.readErr
	}
	return f.before, nil
}
func (f *fakeFollowPage) click(context.Context, string, followButton) error {
	f.clicks++
	if f.onClick != nil {
		f.onClick()
	}
	return f.clickErr
}
func (f *fakeFollowPage) verify(context.Context, string) (followSnapshot, error) {
	if f.persisted != nil {
		return *f.persisted, f.verifyErr
	}
	return f.after, f.verifyErr
}
func followFixture(text string) followSnapshot {
	return followSnapshot{TargetID: "target", ActorID: "fixture-actor", Buttons: []followButton{{Text: text}}}
}

func TestFollowStateAndIdentity(t *testing.T) {
	for _, tc := range []struct{ text, want string }{{" + 关注 ", "unfollowed"}, {"回关", "follows_me"}, {"已关注", "following"}, {"互相关注", "mutual"}, {"相互关注", "mutual"}, {"关注我们", ""}, {"取消关注", ""}} {
		require.Equal(t, tc.want, followState(tc.text))
	}
	for _, tc := range []struct {
		id, token string
		valid     bool
	}{{"abc123", "token", true}, {"../other", "token", false}, {"a?b", "token", false}, {"", "token", false}, {"target", "", false}, {"target", "x\ny", false}} {
		if tc.valid {
			require.NoError(t, ValidateFollowIdentity(tc.id, tc.token))
		} else {
			require.Error(t, ValidateFollowIdentity(tc.id, tc.token))
		}
	}
}
func TestFollowNeverClicksWrongOrAmbiguousTarget(t *testing.T) {
	cases := map[string]followSnapshot{"wrong_target": {TargetID: "other", Buttons: []followButton{{Text: "关注"}}}, "identity_error": {TargetID: "target", IdentityError: "stale", Buttons: []followButton{{Text: "关注"}}}, "self": {TargetID: "target", Self: true, Buttons: []followButton{{Text: "关注"}}}, "missing": {TargetID: "target"}, "ambiguous": {TargetID: "target", Buttons: []followButton{{Text: "关注"}, {Text: "关注"}}}, "disabled": {TargetID: "target", Buttons: []followButton{{Text: "关注", Disabled: true}}}}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			page := &fakeFollowPage{before: s}
			result, err := performFollow(context.Background(), page, "target", false, time.Millisecond)
			require.Error(t, err)
			require.Nil(t, result)
			require.Zero(t, page.clicks)
		})
	}
}
func TestFollowIdempotentAndBackOnly(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		back                bool
		wantErr             bool
		clicks              int
	}{{"already_following", "已关注", "", false, false, 0}, {"already_mutual", "互相关注", "", true, false, 0}, {"follow", "关注", "已关注", false, false, 1}, {"back", "回关", "互相关注", true, false, 1}, {"not_follower", "关注", "", true, true, 0}, {"unknown_mutual", "已关注", "", true, true, 0}, {"not_verified", "回关", "回关", true, true, 1}, {"back_not_mutual", "回关", "已关注", true, true, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			page := &fakeFollowPage{before: followFixture(tc.before), after: followFixture(tc.after)}
			result, err := performFollow(context.Background(), page, "target", tc.back, time.Millisecond)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.True(t, result.Following)
				require.Equal(t, tc.clicks == 1, result.Changed)
			}
			require.Equal(t, tc.clicks, page.clicks)
		})
	}
}
func TestFollowClickFailureAndCancellation(t *testing.T) {
	page := &fakeFollowPage{before: followFixture("关注"), after: followFixture("已关注"), clickErr: errors.New("stale button")}
	result, err := performFollow(context.Background(), page, "target", false, time.Millisecond)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, page.clicks)
	var unconfirmed *FollowUnconfirmedError
	require.ErrorAs(t, err, &unconfirmed)
	require.Contains(t, err.Error(), "retry_safe=false")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	page = &fakeFollowPage{before: followFixture("关注"), after: followFixture("关注")}
	_, err = performFollow(ctx, page, "target", false, time.Second)
	require.ErrorIs(t, err, context.Canceled)
}
func TestFollowOptimisticUpdateAndPostClickCancellation(t *testing.T) {
	reverted := followFixture("关注")
	for _, tc := range []struct {
		name             string
		persisted        *followSnapshot
		verifyErr        error
		cancelAfterClick bool
	}{
		{name: "optimistic_reverted", persisted: &reverted},
		{name: "reload_failed", verifyErr: errors.New("reload failed")},
		{name: "cancel_after_click", cancelAfterClick: true, verifyErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			page := &fakeFollowPage{before: followFixture("关注"), after: followFixture("已关注"), persisted: tc.persisted, verifyErr: tc.verifyErr}
			if tc.cancelAfterClick {
				page.onClick = cancel
			}
			result, err := performFollow(ctx, page, "target", false, time.Millisecond)
			require.Nil(t, result)
			var unconfirmed *FollowUnconfirmedError
			require.ErrorAs(t, err, &unconfirmed)
			require.Contains(t, err.Error(), "retry_safe=false")
			require.Equal(t, 1, page.clicks)
		})
	}
}
