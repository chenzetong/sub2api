package service

import (
	"context"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentContextWSPassthroughRewritesEveryTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(cfg, upstream)
	account := &Account{
		ID:          98701,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test", "chatgpt_account_id": "test-account"},
		Extra:       map[string]any{"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough},
		Proxy:       &Proxy{ID: 88, Extra: map[string]any{"timezone": "America/Los_Angeles"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
	defer server.Close()
	payload := `{"type":"response.create","model":"gpt-5.1","instructions":"test","input":[{"role":"user","content":"<environment_context><cwd>/Users/mike/Personal</cwd><timezone>Asia/Shanghai</timezone><current_date>2000-01-01</current_date></environment_context>"}]}`
	conn := dialPassthroughLifecycleClientWithPayload(t, server, payload)
	defer func() { _ = conn.CloseNow() }()
	first := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	assert.Contains(t, string(first), "<timezone>America/Los_Angeles</timezone>")
	assert.NotContains(t, string(first), "2000-01-01")
	assert.Contains(t, string(first), "<cwd>/Users/mike/Personal</cwd>")
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_test_one","model":"gpt-5.1","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
	_, err := readPassthroughLifecycleFrame(t, conn, 3*time.Second)
	require.NoError(t, err)
	update := []byte(`{"type":"session.update","session":{"instructions":"<environment_context><timezone>Asia/Shanghai</timezone><current_date>2000-01-01</current_date></environment_context>"}}`)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, update))
	forwardedUpdate := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	assert.Contains(t, string(forwardedUpdate), "<timezone>America/Los_Angeles</timezone>")
	assert.NotContains(t, string(forwardedUpdate), "2000-01-01")
	require.NoError(t, conn.Write(ctx, coderws.MessageBinary, []byte(payload)))
	second := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
	assert.Contains(t, string(second), "<timezone>America/Los_Angeles</timezone>")
	assert.NotContains(t, string(second), "2000-01-01")
	cancel()
	_ = conn.CloseNow()
	select {
	case <-serverErr:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown timeout")
	}
}
