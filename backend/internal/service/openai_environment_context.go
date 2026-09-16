package service

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/tidwall/gjson"
)

const (
	environmentContextTag      = "<environment_context>"
	environmentContextCloseTag = "</environment_context>"
	timezoneOpenTag            = "<timezone>"
	timezoneCloseTag           = "</timezone>"
	currentDateOpenTag         = "<current_date>"
	currentDateCloseTag        = "</current_date>"
	proxyTimezoneExtraKey      = "timezone"
)

// RewriteEnvironmentContextTimezone replaces the timezone and current date in
// instruction and user-message text. Tool data and assistant output are not
// environment declarations. Unchanged requests retain their original bytes.
func RewriteEnvironmentContextTimezone(body []byte, timezone string, now time.Time) ([]byte, bool) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return body, false
	}
	if len(body) == 0 {
		return body, false
	}
	root := gjson.ParseBytes(body)
	if !root.Exists() || !gjson.ValidBytes(body) {
		return body, false
	}
	replacements := make([]environmentContextReplacement, 0, 2)
	collectEnvironmentContextReplacements(body, root, location, now, &replacements)
	if len(replacements) == 0 {
		return body, false
	}
	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].start < replacements[j].start
	})
	var rewritten bytes.Buffer
	rewritten.Grow(len(body))
	previous := 0
	for _, replacement := range replacements {
		if replacement.start < previous || replacement.end > len(body) || replacement.start > replacement.end {
			return body, false
		}
		rewritten.Write(body[previous:replacement.start])
		rewritten.Write(replacement.value)
		previous = replacement.end
	}
	rewritten.Write(body[previous:])
	return rewritten.Bytes(), true
}

type environmentContextReplacement struct {
	start int
	end   int
	value []byte
}

// Only visit prompt-bearing fields. Recursing through arbitrary JSON strings
// would also rewrite tool schemas, call arguments and tool results.
func collectEnvironmentContextReplacements(body []byte, request gjson.Result, location *time.Location, now time.Time, replacements *[]environmentContextReplacement) {
	collectText := func(value gjson.Result) {
		collectEnvironmentContextTextReplacements(body, value, location, now, replacements)
	}
	collectText(request.Get("instructions"))
	collectText(request.Get("system"))
	for _, field := range []string{"input", "messages"} {
		messages := request.Get(field)
		if messages.Type == gjson.String {
			collectText(messages)
		} else if messages.IsArray() {
			messages.ForEach(func(_, message gjson.Result) bool {
				switch message.Get("role").String() {
				case "user", "system", "developer":
					collectText(message.Get("content"))
				case "":
					// Also accept direct input_text blocks used by compatibility clients.
					collectText(message)
				}
				return true
			})
		}
	}
	if response := request.Get("response"); response.IsObject() {
		collectEnvironmentContextReplacements(body, response, location, now, replacements)
	}
	collectText(request.Get("session.instructions"))
}

func collectEnvironmentContextTextReplacements(body []byte, value gjson.Result, location *time.Location, now time.Time, replacements *[]environmentContextReplacement) {
	switch value.Type {
	case gjson.String:
		rewritten, changed := rewriteEnvironmentContextText(value.String(), location, now)
		if !changed {
			return
		}
		encoded, err := marshalJSONString(rewritten)
		if err != nil {
			return
		}
		if value.Index < 0 || value.Index+len(value.Raw) > len(body) {
			// Calculated gjson results have no stable byte range in the input.
			return
		}
		*replacements = append(*replacements, environmentContextReplacement{
			start: value.Index,
			end:   value.Index + len(value.Raw),
			value: encoded,
		})
	case gjson.JSON:
		if value.IsArray() {
			value.ForEach(func(_, item gjson.Result) bool {
				collectEnvironmentContextTextReplacements(body, item, location, now, replacements)
				return true
			})
		} else if kind := value.Get("type").String(); kind == "text" || kind == "input_text" {
			collectEnvironmentContextTextReplacements(body, value.Get("text"), location, now, replacements)
		}
	}
}

func marshalJSONString(value string) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte("\n")), nil
}

func rewriteEnvironmentContextText(text string, location *time.Location, now time.Time) (string, bool) {
	var rewritten strings.Builder
	date := now.In(location).Format("2006-01-02")
	written, scan := 0, 0
	changed := false
	for scan < len(text) {
		start := strings.Index(text[scan:], environmentContextTag)
		if start < 0 {
			break
		}
		start += scan + len(environmentContextTag)
		end := strings.Index(text[start:], environmentContextCloseTag)
		if end < 0 {
			break
		}
		end += start
		scan = end + len(environmentContextCloseTag)
		block := text[start:end]
		// Nested opening tags indicate malformed context; leave it intact.
		if strings.Contains(block, environmentContextTag) {
			continue
		}
		block, timezoneChanged := replaceTagText(block, timezoneOpenTag, timezoneCloseTag, location.String())
		block, dateChanged := replaceTagText(block, currentDateOpenTag, currentDateCloseTag, date)
		if timezoneChanged || dateChanged {
			rewritten.WriteString(text[written:start])
			rewritten.WriteString(block)
			written = end
			changed = true
		}
	}
	if !changed {
		return text, false
	}
	rewritten.WriteString(text[written:])
	return rewritten.String(), true
}

func replaceTagText(value, openTag, closeTag, replacement string) (string, bool) {
	var rewritten strings.Builder
	written, scan := 0, 0
	changed := false
	for scan < len(value) {
		start := strings.Index(value[scan:], openTag)
		if start < 0 {
			break
		}
		start += scan + len(openTag)
		end := strings.Index(value[start:], closeTag)
		if end < 0 {
			break
		}
		end += start
		scan = end + len(closeTag)
		if strings.Contains(value[start:end], "<") || value[start:end] == replacement {
			continue
		}
		rewritten.WriteString(value[written:start])
		rewritten.WriteString(replacement)
		written = end
		changed = true
	}
	if !changed {
		return value, false
	}
	rewritten.WriteString(value[written:])
	return rewritten.String(), true
}

// RewriteEnvironmentContextForAccount aligns the environment context in body
// with the selected account's proxy exit timezone. Unknown or invalid timezones
// leave the body unchanged.
func (s *OpenAIGatewayService) RewriteEnvironmentContextForAccount(ctx context.Context, account *Account, body []byte) []byte {
	timezone := accountEnvironmentTimezone(ctx, s, account)
	if timezone == "" {
		return body
	}
	rewritten, changed := RewriteEnvironmentContextTimezone(body, timezone, time.Now())
	if !changed {
		return body
	}
	return rewritten
}

func accountEnvironmentTimezone(ctx context.Context, gateway *OpenAIGatewayService, account *Account) string {
	if account == nil {
		return ""
	}
	if account.Proxy != nil && account.Proxy.Extra != nil {
		if timezone, ok := account.Proxy.Extra[proxyTimezoneExtraKey].(string); ok {
			if timezone = strings.TrimSpace(timezone); timezone != "" {
				if _, err := time.LoadLocation(timezone); err == nil {
					return timezone
				}
			}
		}
	}
	proxyID := int64(0)
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	} else if account.Proxy != nil {
		proxyID = account.Proxy.ID
	}
	if proxyID <= 0 || gateway == nil || gateway.proxyLatencyCache == nil {
		return ""
	}
	observations, err := gateway.proxyLatencyCache.GetProxyLatencies(ctx, []int64{proxyID})
	if err != nil || observations == nil || observations[proxyID] == nil {
		return ""
	}
	return strings.TrimSpace(observations[proxyID].Timezone)
}
