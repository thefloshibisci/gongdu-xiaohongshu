package xiaohongshu

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestFollowObservationNeverClaimsTargetAccountVerified(t *testing.T) {
	before, after := followFixture("回关"), followFixture("互相关注")
	after.Relationship = "friends"
	page := &fakeFollowPage{before: before, after: after}
	r, err := performFollow(context.Background(), page, "target", true, time.Second)
	require.NoError(t, err)
	data, err := json.Marshal(r)
	require.NoError(t, err)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(data, &obj))
	require.Equal(t, "current_account_observation", obj["status"])
	require.Equal(t, false, obj["target_account_verified"])
	require.Equal(t, false, obj["retry_safe"])
	require.Equal(t, "fixture-actor", obj["actor_user_id_before"])
	require.Equal(t, "friends", obj["profile_store_relationship"])
	for _, key := range []string{"following", "mutual", "changed", "verified_by"} {
		require.NotContains(t, obj, key)
	}
	require.Equal(t, 1, page.clicks)
}

func TestFollowMissingOrChangedActorCannotBeAccepted(t *testing.T) {
	for _, missing := range []bool{true, false} {
		before, after := followFixture("关注"), followFixture("已关注")
		if missing {
			before.ActorID = ""
		} else {
			after.ActorID = "different-actor"
		}
		page := &fakeFollowPage{before: before, after: after}
		result, err := performFollow(context.Background(), page, "target", false, time.Second)
		require.Nil(t, result)
		require.Error(t, err)
		if missing {
			require.Zero(t, page.clicks)
		} else {
			require.Equal(t, 1, page.clicks)
			require.Contains(t, err.Error(), "retry_safe=false")
		}
	}
}

func TestFollowRequestEvidenceIsScopedAndDoesNotRetainSecrets(t *testing.T) {
	body := `{"target_user_id":"target","xsec_token":"secret-token","content":"private-text"}`
	r := followRequestSummary("https://edith.xiaohongshu.com/api/sns/web/v1/user/follow?secret=query", "POST", body, "target")
	require.NotNil(t, r)
	require.True(t, r.TargetMatched)
	require.Len(t, r.BodySHA256, 64)
	data, err := json.Marshal(r)
	require.NoError(t, err)
	for _, secret := range []string{"secret-token", "private-text", "secret=query"} {
		require.NotContains(t, string(data), secret)
	}
	require.False(t, followRequestSummary("https://edith.xiaohongshu.com/follow", "POST", body, "other").TargetMatched)
	for _, tc := range []struct{ u, m string }{
		{"https://xiaohongshu.com.evil.test/follow", "POST"},
		{"https://edith.xiaohongshu.com/profile", "POST"},
		{"https://edith.xiaohongshu.com/follow", "GET"},
	} {
		require.Nil(t, followRequestSummary(tc.u, tc.m, body, "target"))
	}
}
