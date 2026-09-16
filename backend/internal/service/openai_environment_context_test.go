package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteEnvironmentContextTimezone(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	body := []byte(`{
		"instructions":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>",
		"messages":[{"role":"user","content":[{"type":"text","text":"<environment_context><timezone>Asia/Shanghai</timezone><current_date>2026-09-13</current_date></environment_context>"},{"type":"text","text":"plain text"}]}]
	}`)

	rewritten, changed := RewriteEnvironmentContextTimezone(body, "America/Los_Angeles", now)
	require.True(t, changed)
	require.Equal(t, 3, strings.Count(string(rewritten), `<timezone>America/Los_Angeles</timezone>`))
	require.Equal(t, 3, strings.Count(string(rewritten), `<current_date>2026-09-15</current_date>`))
	require.NotContains(t, string(rewritten), `Asia/Shanghai`)
	require.Contains(t, string(rewritten), `"plain text"`)
}

func TestRewriteEnvironmentContextTimezonePreservesUnrelatedJSON(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	body := []byte(`{"id":9007199254740993,"keep":{"escaped":"\u003cunchanged\u003e"},"input":"<environment_context><timezone>Asia/Shanghai</timezone><current_date>2026-09-13</current_date></environment_context>"}`)

	rewritten, changed := RewriteEnvironmentContextTimezone(body, "UTC", now)
	require.True(t, changed)
	require.Contains(t, string(rewritten), `"id":9007199254740993`)
	require.Contains(t, string(rewritten), `"escaped":"\u003cunchanged\u003e"`)
	require.Contains(t, string(rewritten), `<timezone>UTC</timezone>`)
}

func TestRewriteEnvironmentContextTimezoneLeavesOtherContentUntouched(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	body := []byte(`{"messages":[{"role":"user","content":"<cwd>/workspace</cwd><timezone>Asia/Shanghai</timezone>"}]}`)

	rewritten, changed := RewriteEnvironmentContextTimezone(body, "America/New_York", now)
	require.False(t, changed)
	require.Equal(t, body, rewritten)
}

func TestRewriteEnvironmentContextTimezoneRejectsInvalidTimezone(t *testing.T) {
	body := []byte(`{"input":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}`)
	rewritten, changed := RewriteEnvironmentContextTimezone(body, "Not/A_Timezone", time.Now())
	require.False(t, changed)
	require.Equal(t, body, rewritten)
}

func TestRewriteEnvironmentContextForAccount(t *testing.T) {
	proxyID := int64(42)
	cache := &environmentContextProxyLatencyCache{timezone: "America/Los_Angeles"}
	svc := &OpenAIGatewayService{proxyLatencyCache: cache}
	account := &Account{
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: proxyID},
	}
	body := []byte(`{"input":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}`)

	rewritten := svc.RewriteEnvironmentContextForAccount(context.Background(), account, body)
	require.Contains(t, string(rewritten), `America/Los_Angeles`)
	require.NotContains(t, string(rewritten), `Asia/Shanghai`)
}

func TestRewriteEnvironmentContextForAccountCacheFallbackWithoutLoadedProxy(t *testing.T) {
	proxyID := int64(42)
	cache := &environmentContextProxyLatencyCache{timezone: "America/Los_Angeles"}
	svc := &OpenAIGatewayService{proxyLatencyCache: cache}
	account := &Account{ProxyID: &proxyID}
	body := []byte(`{"input":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}`)

	rewritten := svc.RewriteEnvironmentContextForAccount(context.Background(), account, body)
	require.Contains(t, string(rewritten), `America/Los_Angeles`)
	location, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	expectedDate := time.Now().In(location).Format("2006-01-02")
	require.Contains(t, string(rewritten), expectedDate)
}

func TestRewriteEnvironmentContextForAccountPrefersProxyExtraTimezone(t *testing.T) {
	proxyID := int64(42)
	cache := &environmentContextProxyLatencyCache{timezone: "America/New_York"}
	svc := &OpenAIGatewayService{proxyLatencyCache: cache}
	account := &Account{
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: proxyID, Extra: map[string]any{"timezone": "America/Los_Angeles"}},
	}
	body := []byte(`{"input":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}`)

	rewritten := svc.RewriteEnvironmentContextForAccount(context.Background(), account, body)
	require.Contains(t, string(rewritten), `America/Los_Angeles`)
	require.NotContains(t, string(rewritten), `America/New_York`)
}

func TestRewriteEnvironmentContextForAccountFallsBackFromInvalidProxyExtraTimezone(t *testing.T) {
	proxyID := int64(42)
	cache := &environmentContextProxyLatencyCache{timezone: "America/New_York"}
	svc := &OpenAIGatewayService{proxyLatencyCache: cache}
	account := &Account{
		ProxyID: &proxyID,
		Proxy:   &Proxy{ID: proxyID, Extra: map[string]any{"timezone": "Not/A_Timezone"}},
	}
	body := []byte(`{"input":"<environment_context><current_date>2026-09-13</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}`)

	rewritten := svc.RewriteEnvironmentContextForAccount(context.Background(), account, body)
	require.Contains(t, string(rewritten), `America/New_York`)
	require.NotContains(t, string(rewritten), `Not/A_Timezone`)
}

type environmentContextProxyLatencyCache struct {
	timezone string
}

func (c *environmentContextProxyLatencyCache) GetProxyLatencies(_ context.Context, proxyIDs []int64) (map[int64]*ProxyLatencyInfo, error) {
	result := make(map[int64]*ProxyLatencyInfo, len(proxyIDs))
	for _, proxyID := range proxyIDs {
		result[proxyID] = &ProxyLatencyInfo{Timezone: c.timezone}
	}
	return result, nil
}

func (c *environmentContextProxyLatencyCache) SetProxyLatency(context.Context, int64, *ProxyLatencyInfo) error {
	return nil
}

func (c *environmentContextProxyLatencyCache) DeleteProxyLatency(context.Context, int64) error {
	return nil
}

func TestRewriteEnvironmentContextTimezoneBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	location, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	original := `<environment_context><timezone>Asia/Shanghai</timezone><current_date>2000-01-01</current_date></environment_context>`
	expected := `<environment_context><timezone>America/Los_Angeles</timezone><current_date>2026-09-15</current_date></environment_context>`
	outside := `<timezone>Asia/Tokyo</timezone><current_date>1999-12-31</current_date>`
	cases := []struct {
		name, input, want string
		changed           bool
	}{
		{"outside before and after", outside + original + outside, outside + expected + outside, true},
		{"multiple complete blocks", original + outside + original, expected + outside + expected, true},
		{"missing context close", `<environment_context><timezone>Asia/Shanghai</timezone>`, `<environment_context><timezone>Asia/Shanghai</timezone>`, false},
		{"nested malformed context", `<environment_context>` + original + `</environment_context>`, `<environment_context>` + original + `</environment_context>`, false},
		{"incomplete date preserves following data", `<environment_context><current_date>old<note>keep</note></environment_context>`, `<environment_context><current_date>old<note>keep</note></environment_context>`, false},
		{"no insertion", `<environment_context><cwd>/Users/mike/Personal</cwd></environment_context>`, `<environment_context><cwd>/Users/mike/Personal</cwd></environment_context>`, false},
		{"already correct", expected, expected, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := rewriteEnvironmentContextText(tt.input, location, now)
			require.Equal(t, tt.changed, changed)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRewriteEnvironmentContextTimezoneOnlyVisitsPromptText(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	text := `<environment_context><timezone>Asia/Shanghai</timezone><current_date>2000-01-01</current_date></environment_context>`
	expected := `<environment_context><timezone>America/Los_Angeles</timezone><current_date>2026-09-15</current_date></environment_context>`
	cases := []struct {
		name    string
		request map[string]any
		path    string
		rewrite bool
	}{
		{"instructions", map[string]any{"instructions": text}, "instructions", true},
		{"anthropic system", map[string]any{"system": []any{map[string]any{"type": "text", "text": text}}}, "system.0.text", true},
		{"responses string input", map[string]any{"input": text}, "input", true},
		{"responses user text", map[string]any{"input": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}}, "input.0.content.0.text", true},
		{"compatibility text input", map[string]any{"input": []any{map[string]any{"type": "input_text", "text": text}}}, "input.0.text", true},
		{"chat developer message", map[string]any{"messages": []any{map[string]any{"role": "developer", "content": text}}}, "messages.0.content", true},
		{"nested response", map[string]any{"response": map[string]any{"instructions": text}}, "response.instructions", true},
		{"session update", map[string]any{"session": map[string]any{"instructions": text}}, "session.instructions", true},
		{"tool description", map[string]any{"tools": []any{map[string]any{"type": "function", "description": text}}}, "tools.0.description", false},
		{"metadata", map[string]any{"metadata": map[string]any{"example": text}}, "metadata.example", false},
		{"assistant history", map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": text}}}, "messages.0.content", false},
		{"tool message", map[string]any{"messages": []any{map[string]any{"role": "tool", "content": text}}}, "messages.0.content", false},
		{"function arguments", map[string]any{"input": []any{map[string]any{"type": "function_call", "arguments": text}}}, "input.0.arguments", false},
		{"function output", map[string]any{"input": []any{map[string]any{"type": "function_call_output", "output": text}}}, "input.0.output", false},
		{"anthropic tool result", map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": text}}}}}, "messages.0.content.0.content", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.request)
			require.NoError(t, err)
			got, changed := RewriteEnvironmentContextTimezone(body, "America/Los_Angeles", now)
			require.Equal(t, tt.rewrite, changed)
			want := text
			if tt.rewrite {
				want = expected
			} else {
				require.Equal(t, body, got)
			}
			require.Equal(t, want, gjson.GetBytes(got, tt.path).String())
		})
	}
}

func TestRewriteEnvironmentContextTimezoneRepeatedTagsAreIdempotent(t *testing.T) {
	now := time.Date(2026, 9, 16, 1, 30, 0, 0, time.UTC)
	body, err := json.Marshal(map[string]any{"input": "<environment_context>" + strings.Repeat("<timezone>Asia/Shanghai</timezone>", 1024) + "</environment_context>"})
	require.NoError(t, err)
	rewritten, changed := RewriteEnvironmentContextTimezone(body, "America/Los_Angeles", now)
	require.True(t, changed)
	require.Equal(t, 1024, strings.Count(string(rewritten), "<timezone>America/Los_Angeles</timezone>"))
	again, changed := RewriteEnvironmentContextTimezone(rewritten, "America/Los_Angeles", now)
	require.False(t, changed)
	require.Equal(t, rewritten, again)
}
