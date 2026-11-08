package xiaohongshu

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"net/url"
	"strings"
	"sync"
)

// Evidence describes observed traffic only. It never replays requests, stores
// authentication headers/tokens, or equates an ACK with target-account visibility.
type followRequestEvidence struct {
	Path          string `json:"path"`
	Method        string `json:"method"`
	TargetMatched bool   `json:"request_target_matched"`
	BodySHA256    string `json:"request_body_sha256"`
	HTTPStatus    int    `json:"http_status"`
	Code          *int   `json:"response_code,omitempty"`
	Success       *bool  `json:"response_success,omitempty"`
	Complete      bool   `json:"response_complete"`
}

func followRequestSummary(rawURL, method, body, target string) *followRequestEvidence {
	u, err := url.Parse(rawURL)
	if err != nil || method != "POST" || !(u.Hostname() == "xiaohongshu.com" || strings.HasSuffix(u.Hostname(), ".xiaohongshu.com")) || !strings.Contains(strings.ToLower(u.Path), "follow") {
		return nil
	}
	hash := sha256.Sum256([]byte(body))
	result := &followRequestEvidence{Path: u.Path, Method: method, BodySHA256: hex.EncodeToString(hash[:])}
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &values) == nil {
		for _, key := range []string{"user_id", "userId", "target_user_id", "targetUserId", "followee_id"} {
			var id string
			if json.Unmarshal(values[key], &id) == nil && id == target {
				result.TargetMatched = true
			}
		}
	}
	return result
}

func watchFollowEvidence(page *rod.Page, target string) (func() []followRequestEvidence, func()) {
	p, cancel := page.WithCancel()
	var mu sync.Mutex
	entries := make([]followRequestEvidence, 0, 4)
	pending := map[proto.NetworkRequestID]int{}
	wait := p.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		if followRequestSummary(e.Request.URL, e.Request.Method, "", target) == nil {
			return
		}
		body := e.Request.PostData
		if body == "" && e.Request.HasPostData {
			if r, err := (proto.NetworkGetRequestPostData{RequestID: e.RequestID}).Call(p); err == nil {
				body = r.PostData
			}
		}
		item := followRequestSummary(e.Request.URL, e.Request.Method, body, target)
		if item == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(entries) >= 4 {
			return
		}
		pending[e.RequestID] = len(entries)
		entries = append(entries, *item)
	}, func(e *proto.NetworkResponseReceived) {
		mu.Lock()
		defer mu.Unlock()
		if i, ok := pending[e.RequestID]; ok {
			entries[i].HTTPStatus = e.Response.Status
		}
	}, func(e *proto.NetworkLoadingFinished) {
		mu.Lock()
		i, ok := pending[e.RequestID]
		mu.Unlock()
		if !ok {
			return
		}
		r, err := (proto.NetworkGetResponseBody{RequestID: e.RequestID}).Call(p)
		if err != nil {
			return
		}
		body := []byte(r.Body)
		if r.Base64Encoded {
			body, err = base64.StdEncoding.DecodeString(r.Body)
			if err != nil {
				return
			}
		}
		var v struct {
			Code    *int  `json:"code"`
			Success *bool `json:"success"`
		}
		_ = json.Unmarshal(body, &v)
		mu.Lock()
		entries[i].Code = v.Code
		entries[i].Success = v.Success
		entries[i].Complete = true
		mu.Unlock()
	})
	done := make(chan struct{})
	go func() { defer close(done); wait() }()
	return func() []followRequestEvidence {
		mu.Lock()
		defer mu.Unlock()
		return append([]followRequestEvidence{}, entries...)
	}, func() { cancel(); <-done }
}
