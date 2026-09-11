package reddit

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// The block page Reddit serves on a logged-out API request is ~190 KB of
// minified CSS. Slicing it into an error message is what made the old failure
// mode unreadable, so the classifier must never put it in Detail.
const blockPage = `<body class=theme-beta><div><style>.theme-light,:root{--rem360:22.5rem;--rem320:20rem;`

func TestClassifyBlockPage(t *testing.T) {
	err := classify(http.StatusForbidden, "text/html", []byte(blockPage), false)

	if err.Kind != KindBlocked {
		t.Errorf("kind = %q, want %q", err.Kind, KindBlocked)
	}

	if !IsBlocked(err) {
		t.Error("IsBlocked must report true so callers can fall back to RSS")
	}

	if strings.Contains(err.Error(), "theme-beta") || strings.Contains(err.Error(), "--rem360") {
		t.Errorf("html leaked into the error message: %s", err.Error())
	}

	if !strings.Contains(err.Error(), "REDDIT_CLIENT_ID") {
		t.Errorf("error must say how to fix it, got: %s", err.Error())
	}

	if err.Retryable() {
		t.Error("a block is not fixed by retrying")
	}
}

func TestClassifyBlockPageAuthenticated(t *testing.T) {
	err := classify(http.StatusForbidden, "text/html", []byte(blockPage), true)

	if !strings.Contains(err.Error(), "REDDIT_USER_AGENT") {
		t.Errorf("an authenticated block should point at the app setup, got: %s", err.Error())
	}

	if strings.Contains(err.Error(), "REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET to use") {
		t.Error("telling an already-authenticated caller to set credentials is useless advice")
	}
}

func TestClassifyPrivateSubreddit(t *testing.T) {
	body := []byte(`{"reason": "private", "message": "Forbidden", "error": 403}`)

	err := classify(http.StatusForbidden, "application/json; charset=UTF-8", body, true)

	if err.Kind != KindForbidden {
		t.Errorf("kind = %q, want %q", err.Kind, KindForbidden)
	}

	if IsBlocked(err) {
		t.Error("a private subreddit is not a network block; RSS will not help")
	}

	if !strings.Contains(err.Error(), "private") {
		t.Errorf("reason dropped from error: %s", err.Error())
	}
}

func TestClassifyKinds(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantKind  ErrorKind
		retryable bool
	}{
		{"not found", http.StatusNotFound, KindNotFound, false},
		{"legal", http.StatusUnavailableForLegalReasons, KindUnavailable, false},
		{"rate limited", http.StatusTooManyRequests, KindRateLimited, true},
		{"unauthorized", http.StatusUnauthorized, KindUnauthorized, true},
		{"server", http.StatusBadGateway, KindServer, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classify(tc.status, "application/json", []byte(`{}`), true)

			if err.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", err.Kind, tc.wantKind)
			}

			if err.Retryable() != tc.retryable {
				t.Errorf("retryable = %v, want %v", err.Retryable(), tc.retryable)
			}

			if err.Hint == "" {
				t.Error("every classified error should tell the caller what to do")
			}
		})
	}
}

func TestAPIErrorIsDoesNotMatchOtherSentinels(t *testing.T) {
	err := classify(http.StatusNotFound, "application/json", []byte(`{}`), false)

	if errors.Is(err, errBlocked) {
		t.Error("only blocks should match the blocked sentinel")
	}
}
