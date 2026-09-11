package reddit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorKind classifies a failed Reddit response into something a model can act
// on. Reddit signals very different situations with the same status code — a
// 403 is both "this network is blocked" and "this subreddit is private" — so
// the status alone is not enough to tell the caller what to do next.
type ErrorKind string

const (
	// KindBlocked means Reddit refused the request itself rather than the
	// resource: logged-out API access is denied from this network, and the
	// response is Reddit's HTML block page instead of JSON.
	KindBlocked ErrorKind = "blocked"
	// KindForbidden means the resource exists but is not readable with the
	// current credentials: private, quarantined or gated.
	KindForbidden ErrorKind = "forbidden"
	// KindNotFound means there is no such subreddit, user or post.
	KindNotFound ErrorKind = "not_found"
	// KindUnavailable means Reddit withheld the content for legal reasons.
	KindUnavailable ErrorKind = "unavailable"
	// KindRateLimited means the rate limit was exceeded.
	KindRateLimited ErrorKind = "rate_limited"
	// KindUnauthorized means the OAuth token was rejected.
	KindUnauthorized ErrorKind = "unauthorized"
	// KindServer means Reddit failed on its side.
	KindServer ErrorKind = "server_error"
	// KindUnknown covers everything else.
	KindUnknown ErrorKind = "unknown"
)

// errBlocked is the sentinel behind [IsBlocked], so callers can use errors.Is
// without reaching for the concrete type.
var errBlocked = errors.New("reddit json api blocked")

// APIError is a failed Reddit response. Detail never carries raw HTML: Reddit's
// block page is ~190 KB of minified CSS, and putting a slice of it in an error
// message tells the model nothing about what went wrong.
type APIError struct {
	Status int
	Kind   ErrorKind
	Hint   string
	Detail string
}

func (e *APIError) Error() string {
	var b strings.Builder

	b.WriteString("reddit ")
	b.WriteString(string(e.Kind))
	fmt.Fprintf(&b, " (http %d)", e.Status)

	if e.Detail != "" {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}

	if e.Hint != "" {
		b.WriteString(". ")
		b.WriteString(e.Hint)
	}

	return b.String()
}

func (e *APIError) Is(target error) bool {
	return target == errBlocked && e.Kind == KindBlocked
}

// Retryable reports whether repeating the same request could plausibly succeed.
func (e *APIError) Retryable() bool {
	switch e.Kind {
	case KindRateLimited, KindServer, KindUnauthorized:
		return true
	case KindBlocked, KindForbidden, KindNotFound, KindUnavailable, KindUnknown:
		return false
	default:
		return false
	}
}

// IsBlocked reports whether err means Reddit refused logged-out API access,
// which is the signal to fall back to the public RSS feeds.
func IsBlocked(err error) bool { return errors.Is(err, errBlocked) }

const (
	blockedHintAnon = "Reddit blocks logged-out Data API requests from most networks. " +
		"Set REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET to use the authenticated API; " +
		"without them only the reduced RSS feeds are available."
	blockedHintAuthed = "Reddit rejected the authenticated request. Check that the app is approved " +
		"and that REDDIT_USER_AGENT identifies a real app and Reddit account."
)

// classify turns a non-2xx response into an APIError. body is the (already
// truncated) response body, used only when it is JSON.
func classify(status int, contentType string, body []byte, authed bool) *APIError {
	apiErr := &APIError{Status: status, Kind: KindUnknown}

	isJSON := strings.Contains(strings.ToLower(contentType), "json")
	if isJSON {
		apiErr.Detail = jsonDetail(body)
	}

	switch {
	case status == http.StatusForbidden && !isJSON:
		// Reddit's HTML block page: the request never reached the API.
		apiErr.Kind = KindBlocked
		apiErr.Hint = blockedHintAnon

		if authed {
			apiErr.Hint = blockedHintAuthed
		}
	case status == http.StatusForbidden:
		apiErr.Kind = KindForbidden
		apiErr.Hint = "The subreddit or profile is private, quarantined or gated; " +
			"it cannot be read with app-only credentials."
	case status == http.StatusNotFound:
		apiErr.Kind = KindNotFound
		apiErr.Hint = "No such subreddit, user or post — check the spelling, " +
			"or the content may have been removed."
	case status == http.StatusUnavailableForLegalReasons:
		apiErr.Kind = KindUnavailable
		apiErr.Hint = "Reddit withheld this content for legal reasons in this region."
	case status == http.StatusTooManyRequests:
		apiErr.Kind = KindRateLimited
		apiErr.Hint = "Rate limit exceeded; the server backs off and retries automatically."
	case status == http.StatusUnauthorized:
		apiErr.Kind = KindUnauthorized
		apiErr.Hint = "The OAuth token was rejected — verify REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET."
	case status >= http.StatusInternalServerError:
		apiErr.Kind = KindServer
		apiErr.Hint = "Reddit is failing on its side; the server retries with backoff."
	}

	if apiErr.Detail == "" && !isJSON {
		apiErr.Detail = "non-JSON response (" + summarizeContentType(contentType) + ")"
	}

	return apiErr
}

// jsonDetail pulls the human-readable part out of a Reddit JSON error body,
// which looks like {"reason": "private", "message": "Forbidden", "error": 403}.
func jsonDetail(body []byte) string {
	var payload struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
		Explain string `json:"explanation"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	parts := make([]string, 0, 3)

	for _, p := range []string{payload.Reason, payload.Message, payload.Explain} {
		if p != "" {
			parts = append(parts, p)
		}
	}

	return truncate(strings.Join(parts, ": "), errMsgLimit)
}

func summarizeContentType(contentType string) string {
	if contentType == "" {
		return "no content-type"
	}

	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}

	return strings.TrimSpace(contentType)
}
