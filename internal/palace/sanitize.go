package palace

import (
	"fmt"
	"regexp"
	"strings"
)

// Input-validation limits and the safe-name pattern, ported verbatim from the
// frozen Python mempalace (config.py) so the Go SaaS rewrite accepts and rejects
// exactly the same wing/room/agent/topic names and the same drawer/diary content
// the original tool did — a divergence here would let an agent file a memory the
// Python palace would have refused, breaking parity.
const (
	// MaxNameLength bounds a wing/room/agent/topic name (frozen MAX_NAME_LENGTH).
	MaxNameLength = 128
	// MaxContentLength bounds drawer/diary content (frozen sanitize_content default).
	MaxContentLength = 100_000
)

// safeNameRE is the frozen _SAFE_NAME_RE translated to RE2: a name must start and
// end with an alphanumeric, with up to 126 interior characters drawn from
// alphanumerics plus space, dot, apostrophe, hyphen and underscore. A single
// alphanumeric is valid; underscore may appear only in the interior. Python's
// `[^\W_]` (a word char that is not underscore) maps to `[\p{L}\p{N}]` and `[\w]`
// to `[\p{L}\p{N}_]`; the Unicode classes preserve the original's acceptance of
// non-ASCII letters and digits.
var safeNameRE = regexp.MustCompile(`^(?:[\p{L}\p{N}]|[\p{L}\p{N}][\p{L}\p{N} .'_-]{0,126}[\p{L}\p{N}])$`)

// SanitizeName validates and trims a wing/room/agent/topic name, returning the
// cleaned value or wrapping ErrInvalidInput with the offending field so the MCP
// layer can surface it as a tool error. The checks mirror the frozen tool in
// order: non-empty after trim, within MaxNameLength, no path-traversal sequence
// (`..`, `/`, `\`) or NUL byte — these are called out explicitly for clear error
// messages even though the pattern would also reject them — then the safe-name
// pattern. field names the argument in the error (e.g. "agent_name").
func SanitizeName(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%w: %s must be a non-empty string", ErrInvalidInput, field)
	}
	// Count runes, not bytes: the frozen limit is a character count, so a
	// multibyte name up to 128 runes must not be rejected for its byte length.
	if len([]rune(value)) > MaxNameLength {
		return "", fmt.Errorf("%w: %s exceeds maximum length of %d characters", ErrInvalidInput, field, MaxNameLength)
	}
	if strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return "", fmt.Errorf("%w: %s contains invalid path characters", ErrInvalidInput, field)
	}
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: %s contains null bytes", ErrInvalidInput, field)
	}
	if !safeNameRE.MatchString(value) {
		return "", fmt.Errorf("%w: %s contains invalid characters", ErrInvalidInput, field)
	}
	return value, nil
}

// SanitizeContent validates drawer/diary content, returning the original value
// (untrimmed — the verbatim text is preserved, only validated) or wrapping
// ErrInvalidInput. It mirrors frozen sanitize_content: reject content that is
// empty/whitespace-only, longer than MaxContentLength, or carries a NUL byte. The
// emptiness test trims, but the returned content is not trimmed, because a drawer
// is stored verbatim and trimming would silently mutate the memory.
func SanitizeContent(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: content must be a non-empty string", ErrInvalidInput)
	}
	if len([]rune(value)) > MaxContentLength {
		return "", fmt.Errorf("%w: content exceeds maximum length of %d characters", ErrInvalidInput, MaxContentLength)
	}
	if strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("%w: content contains null bytes", ErrInvalidInput)
	}
	return value, nil
}
