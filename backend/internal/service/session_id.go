package service

import (
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// maxPersistedSessionIDLength bounds the persisted client session identifier to the
// usage_logs.session_id column width (VARCHAR(255)). Longer values are rejected so
// distinct identifiers can never alias through truncation.
const maxPersistedSessionIDLength = 255

// strictSessionHashPrefix namespaces client-provided session identifiers whose
// upstream-account affinity must never be silently moved to another account.
// Content-derived sticky hashes intentionally remain soft and may still fail over.
const strictSessionHashPrefix = "strict:v1:"

func markStrictSessionHash(sessionHash string) string {
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" || IsStrictSessionHash(sessionHash) {
		return sessionHash
	}
	return strictSessionHashPrefix + sessionHash
}

// IsStrictSessionHash reports whether a sticky cache key was derived from an
// explicit client session header. OpenAI keys add their own "openai:" namespace
// before reaching GatewayCache, so accept that wrapper as well.
func IsStrictSessionHash(sessionHash string) bool {
	sessionHash = strings.TrimSpace(sessionHash)
	sessionHash = strings.TrimPrefix(sessionHash, "openai:")
	return strings.HasPrefix(sessionHash, strictSessionHashPrefix)
}

// clientSessionIDHeaders extends the OpenAI-compatible sticky-session signals with
// native protocol identifiers that are safe to persist but must not alter OpenAI
// scheduling behavior.
var clientSessionIDHeaders = append(
	append([]string(nil), explicitOpenAIHeaderSessionNames...),
	claudeCodeSessionHeader,
)

// ExtractClientSessionID resolves the explicit client-provided session identifier from
// request headers for usage-log correlation and returns it sanitized. It is
// protocol-agnostic and shared by every gateway handler so all supported protocols
// record session_id through one seam. Returns "" when no valid identifier is present.
//
// Gateway handlers also copy this sanitized value into SessionContext so an explicit
// session header receives strict upstream-account affinity. Other consumers use it
// for usage-log correlation only.
func ExtractClientSessionID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	for _, header := range clientSessionIDHeaders {
		if sessionID := sanitizeSessionID(c.GetHeader(header)); sessionID != "" {
			return sessionID
		}
	}
	if isGrokRequestContext(c) {
		if sessionID := sanitizeSessionID(c.GetHeader(grokConversationIDHeader)); sessionID != "" {
			return sessionID
		}
	}
	return ""
}

// sanitizeSessionID normalizes a raw client-supplied session identifier for safe
// persistence: it trims surrounding whitespace, rejects the value outright if it
// contains any control character (CR/LF/tab/NUL/…) so a log- or header-injection style
// payload cannot slip into stored correlation data, and rejects values longer than
// the DB column bound. Absent or invalid input yields "".
func sanitizeSessionID(raw string) string {
	if !utf8.ValidString(raw) {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			// An explicit correlation id never legitimately contains control
			// characters; drop the whole value rather than persist a mangled or
			// partially-injected identifier.
			return ""
		}
		count++
		if count > maxPersistedSessionIDLength {
			return ""
		}
	}
	return trimmed
}
