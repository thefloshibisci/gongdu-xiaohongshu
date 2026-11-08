package main

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFollowToolsRegisteredAndInvalidArgumentsNeverOpenBrowser(t *testing.T) {
	ctx := context.Background()
	server := InitMCPServer(&AppServer{})
	ct, st := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "follow-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	found := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		found[tool.Name] = tool
	}
	require.Contains(t, found, "follow_user")
	require.Contains(t, found, "follow_back")
	require.NotContains(t, found, "unfollow_user")
	for _, name := range []string{"follow_user", "follow_back"} {
		require.True(t, found[name].Annotations.IdempotentHint)
		require.False(t, found[name].Annotations.ReadOnlyHint)
		for _, args := range []string{`{}`, `{"user_id":"../wrong","xsec_token":"fixture"}`, `{"user_id":"target","xsec_token":""}`} {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
			require.NoError(t, err)
			require.True(t, result.IsError)
		}
	}
}
