package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

type FollowUserArgs struct {
	UserID    string `json:"user_id" jsonschema:"目标用户ID，从用户资料或新增关注通知的from.user_id获取"`
	XsecToken string `json:"xsec_token" jsonschema:"目标用户访问令牌，不能用笔记令牌代替"`
}

func registerFollowTools(server *mcp.Server, app *AppServer) {
	for _, backOnly := range []bool{false, true} {
		backOnly := backOnly
		name, description := "follow_user", "关注指定用户；已关注时不重复操作。回执仅是当前账号页面及请求观察，未核实目标账号是否收到关注，不要自动重试。需要用户ID及用户访问令牌。"
		if backOnly {
			name, description = "follow_back", "回关已关注当前账号的指定用户；仅在目标主页明确显示回关时执行，互相关注时跳过。回执仅是当前账号观察，目标账号是否收到关注仍未核实；不要自动重试或批量回关。"
		}
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{Title: name, DestructiveHint: boolPtr(true), IdempotentHint: true}},
			withPanicRecovery(name, func(ctx context.Context, req *mcp.CallToolRequest, args FollowUserArgs) (*mcp.CallToolResult, any, error) {
				if err := xiaohongshu.ValidateFollowIdentity(args.UserID, args.XsecToken); err != nil {
					return followToolError(err.Error()), nil, nil
				}
				if app.xiaohongshuService == nil {
					return followToolError("小红书服务未就绪"), nil, nil
				}
				result, err := app.xiaohongshuService.FollowUser(ctx, args.UserID, args.XsecToken, backOnly)
				if err != nil {
					var unknown *xiaohongshu.FollowUnconfirmedError
					if errors.As(err, &unknown) {
						data, _ := json.Marshal(map[string]any{"status": "submitted_unconfirmed", "retry_safe": false, "user_id": args.UserID, "message": err.Error()})
						return followToolError(string(data)), nil, nil
					}
					return followToolError(err.Error()), nil, nil
				}
				data, err := json.Marshal(result)
				if err != nil {
					return followToolError("无法编码关注回执"), nil, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
			}),
		)
	}
}
func followToolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}
