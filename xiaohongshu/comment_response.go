package xiaohongshu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type commentResponse struct {
	err error
	id  string
}

func commentRequestMatches(rawURL, method, body, feedID, targetID, content string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || method != "POST" || (u.Hostname() != "xiaohongshu.com" && !strings.HasSuffix(u.Hostname(), ".xiaohongshu.com")) {
		return false
	}
	if u.Path != "/api/sns/web/v1/comment/post" && u.Path != "/api/sns/web/v2/comment/post" {
		return false
	}
	var request struct {
		NoteID          string `json:"note_id"`
		Content         string `json:"content"`
		TargetCommentID string `json:"target_comment_id"`
	}
	return json.Unmarshal([]byte(body), &request) == nil && request.NoteID == feedID && request.TargetCommentID == targetID && strings.TrimSpace(request.Content) == strings.TrimSpace(content)
}

func parseCommentResponse(body []byte) commentResponse {
	var response struct {
		Code    *int `json:"code"`
		Success bool `json:"success"`
		Data    *struct {
			ID        string `json:"id"`
			CommentID string `json:"comment_id"`
			Comment   *struct {
				ID string `json:"id"`
			} `json:"comment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return commentResponse{err: fmt.Errorf("无法解析提交响应")}
	}
	if response.Code == nil || *response.Code != 0 || !response.Success {
		return commentResponse{err: fmt.Errorf("提交接口没有返回明确成功标记")}
	}
	if response.Data == nil || (response.Data.ID == "" && response.Data.CommentID == "" && (response.Data.Comment == nil || response.Data.Comment.ID == "")) {
		return commentResponse{err: fmt.Errorf("提交响应缺少新评论ID，无法核实")}
	}
	id := response.Data.CommentID
	if response.Data.Comment != nil && response.Data.Comment.ID != "" {
		id = response.Data.Comment.ID
	}
	if id == "" {
		id = response.Data.ID
	}
	return commentResponse{id: id}
}

// Observe only the actual browser send, never replay it or make a second request.
func watchCommentResponse(page *rod.Page, feedID, targetID, content string) (<-chan commentResponse, func()) {
	p, cancel := page.WithCancel()
	responses := make(chan commentResponse, 1)
	requests := make(map[proto.NetworkRequestID]int)
	update := func(result commentResponse) {
		select {
		case responses <- result:
		default:
		}
	}
	wait := p.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		u, err := url.Parse(e.Request.URL)
		if err != nil || e.Request.Method != "POST" || (u.Hostname() != "xiaohongshu.com" && !strings.HasSuffix(u.Hostname(), ".xiaohongshu.com")) || (u.Path != "/api/sns/web/v1/comment/post" && u.Path != "/api/sns/web/v2/comment/post") {
			return
		}
		body := e.Request.PostData
		if body == "" && e.Request.HasPostData {
			data, err := (proto.NetworkGetRequestPostData{RequestID: e.RequestID}).Call(p)
			if err != nil {
				return
			}
			body = data.PostData
		}
		if commentRequestMatches(e.Request.URL, e.Request.Method, body, feedID, targetID, content) {
			requests[e.RequestID] = 0
		}
	}, func(e *proto.NetworkResponseReceived) {
		if _, ok := requests[e.RequestID]; ok {
			requests[e.RequestID] = e.Response.Status
		}
	}, func(e *proto.NetworkLoadingFinished) {
		status, ok := requests[e.RequestID]
		if !ok {
			return
		}
		delete(requests, e.RequestID)
		if status < 200 || status >= 300 {
			update(commentResponse{err: fmt.Errorf("提交响应HTTP %d", status)})
			return
		}
		response, err := (proto.NetworkGetResponseBody{RequestID: e.RequestID}).Call(p)
		if err != nil {
			update(commentResponse{err: fmt.Errorf("未能读取提交响应")})
			return
		}
		body := []byte(response.Body)
		if response.Base64Encoded {
			body, err = base64.StdEncoding.DecodeString(response.Body)
			if err != nil {
				update(commentResponse{err: err})
				return
			}
		}
		update(parseCommentResponse(body))
	}, func(e *proto.NetworkLoadingFailed) {
		if _, ok := requests[e.RequestID]; ok {
			delete(requests, e.RequestID)
			update(commentResponse{err: fmt.Errorf("提交请求未确认完成")})
		}
	})
	done := make(chan struct{})
	go func() { defer close(done); wait() }()
	return responses, func() { cancel(); <-done }
}

func waitCommentResponse(ctx context.Context, responses <-chan commentResponse, timeout time.Duration) error {
	_, err := waitCommentAcknowledgement(ctx, responses, timeout)
	return err
}

func waitCommentAcknowledgement(ctx context.Context, responses <-chan commentResponse, timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", commentUnconfirmed(ctx.Err())
	case <-timer.C:
		return "", commentUnconfirmed(fmt.Errorf("未收到可确认的新评论提交回执"))
	case result := <-responses:
		if result.err != nil {
			return "", commentUnconfirmed(result.err)
		}
		if result.id == "" {
			return "", commentUnconfirmed(fmt.Errorf("回执没有新评论ID"))
		}
		return result.id, nil
	}
}
