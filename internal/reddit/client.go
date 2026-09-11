package reddit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	anonHost      = "https://www.reddit.com"
	authHost      = "https://oauth.reddit.com"
	oauthEndpoint = "https://www.reddit.com/api/v1/access_token"
	maxRetries    = 3

	httpTimeout = 30 * time.Second
	tokenLeeway = 10 * time.Second

	backoffBaseMS = 100
	backoffCapMS  = 30000

	// defaultRPMAnon is deliberately low: without credentials the only thing
	// that still answers is the public RSS feed, which Reddit throttles per IP
	// far more aggressively than the Data API.
	defaultRPMAnon = 10
	// defaultRPMAuthed matches the 100 queries/minute per OAuth client id that
	// Reddit documents for free Data API access.
	defaultRPMAuthed = 100

	errBodyLimit = 2048
	errMsgLimit  = 200
)

// Options configures a Client. The zero value is a usable anonymous client with
// caching disabled.
type Options struct {
	ClientID      string
	ClientSecret  string
	UserAgent     string
	RateLimitRPM  int
	CacheTTL      time.Duration
	CacheMaxBytes int64
	// ListingTextChars caps post self-text and comment bodies inside listings,
	// where the model is skimming rather than reading. Zero disables the cap.
	ListingTextChars int

	// BaseURL and RSSBaseURL override the Reddit endpoints. They default to
	// Reddit's own hosts and exist so the client can be pointed at a stub or a
	// compatible mirror.
	BaseURL    string
	RSSBaseURL string

	Logger *slog.Logger
}

type Client struct {
	httpClient   *http.Client
	logger       *slog.Logger
	userAgent    string
	clientID     string
	clientSecret string
	baseURL      string
	rssBaseURL   string
	authed       bool

	limiter     *limiter
	cache       *cache
	cacheTTL    time.Duration
	listingText int

	// jsonBlocked latches once Reddit has refused logged-out Data API access.
	// Without it every anonymous call would spend a request discovering the
	// same 403 before falling back to RSS.
	jsonBlocked atomic.Bool

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func New(opts Options) *Client {
	authed := opts.ClientID != "" && opts.ClientSecret != ""

	rpm := opts.RateLimitRPM
	if rpm <= 0 {
		rpm = defaultRPMAnon
		if authed {
			rpm = defaultRPMAuthed
		}
	}

	baseURL := anonHost
	if authed {
		baseURL = authHost
	}

	if opts.BaseURL != "" {
		baseURL = opts.BaseURL
	}

	rssBaseURL := anonHost
	if opts.RSSBaseURL != "" {
		rssBaseURL = opts.RSSBaseURL
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	return &Client{
		httpClient:   &http.Client{Timeout: httpTimeout},
		logger:       logger,
		userAgent:    opts.UserAgent,
		clientID:     opts.ClientID,
		clientSecret: opts.ClientSecret,
		baseURL:      baseURL,
		rssBaseURL:   rssBaseURL,
		authed:       authed,
		limiter:      newLimiter(rpm, logger),
		cache:        newCache(opts.CacheMaxBytes),
		cacheTTL:     opts.CacheTTL,
		listingText:  opts.ListingTextChars,
	}
}

func (c *Client) Authenticated() bool { return c.authed }

// JSONBlocked reports whether Reddit has refused logged-out Data API access in
// this process, meaning results are coming from the reduced RSS feeds.
func (c *Client) JSONBlocked() bool { return c.jsonBlocked.Load() }

// CacheStats exposes counters for the "cache" log line on shutdown and for tests.
func (c *Client) CacheStats() (hits, misses int64, entries int) { return c.cache.stats() }

// get calls the Reddit Data API. When the API is unreachable anonymously it
// returns an error for which IsBlocked reports true, which is the caller's cue
// to try the RSS equivalent.
func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	if !c.authed && c.jsonBlocked.Load() {
		return nil, &APIError{
			Status: http.StatusForbidden,
			Kind:   KindBlocked,
			Detail: "logged-out Data API access is blocked",
			Hint:   blockedHintAnon,
		}
	}

	if params == nil {
		params = url.Values{}
	}

	params.Set("raw_json", "1")

	apiPath := path
	if !c.authed && !strings.HasSuffix(apiPath, ".json") {
		apiPath += ".json"
	}

	body, err := c.fetch(ctx, c.baseURL+apiPath+"?"+params.Encode(), path, "json")
	if err != nil {
		if !c.authed && IsBlocked(err) && c.jsonBlocked.CompareAndSwap(false, true) {
			c.logger.Warn("reddit blocks logged-out Data API access; falling back to public RSS feeds " +
				"(set REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET for full data)")
		}

		return nil, err
	}

	return body, nil
}

// getRSS calls a public Atom feed on www.reddit.com. These feeds answer without
// credentials, but carry no scores, comment counts or ratios.
func (c *Client) getRSS(ctx context.Context, path string, params url.Values) ([]byte, error) {
	if params == nil {
		params = url.Values{}
	}

	return c.fetch(ctx, c.rssBaseURL+path+"?"+params.Encode(), path, "xml")
}

// fetch runs one request through the limiter with retries. expect is the
// substring the response Content-Type must contain.
func (c *Client) fetch(ctx context.Context, fullURL, cachePath, expect string) ([]byte, error) {
	if body, _, ok := c.cache.get(fullURL); ok {
		c.logger.Debug("reddit cache hit", "url", fullURL)

		return body, nil
	}

	c.logger.Debug("reddit GET", "url", fullURL)

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.limiter.wait(ctx); err != nil {
			return nil, fmt.Errorf("rate-limit wait: %w", err)
		}

		body, retry, err := c.attempt(ctx, fullURL, expect, attempt)
		if err == nil {
			c.cache.put(fullURL, body, expect, ttlFor(cachePath, c.cacheTTL))

			return body, nil
		}

		lastErr = err

		if !retry {
			return nil, lastErr
		}

		if attempt == maxRetries {
			break // no point sleeping before giving up
		}

		if berr := c.sleepBackoff(ctx, attempt); berr != nil {
			return nil, fmt.Errorf("backoff: %w", berr)
		}
	}

	return nil, fmt.Errorf("gave up after %d attempts: %w", maxRetries+1, lastErr)
}

// attempt performs a single request, reporting whether a retry is worthwhile.
func (c *Client) attempt(ctx context.Context, fullURL, expect string, attempt int) ([]byte, bool, error) {
	req, err := c.buildRequest(ctx, fullURL)
	if err != nil {
		return nil, false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Debug("reddit transport error", "attempt", attempt, "err", err)

		return nil, true, fmt.Errorf("http do: %w", err)
	}

	c.limiter.observe(resp.Header, parseRetryAfter(resp.Header.Get("Retry-After")))

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		body, readErr := readBody(resp, expect)
		if readErr != nil {
			return nil, false, readErr
		}

		return body, false, nil
	}

	retry, statusErr := c.handleNonSuccess(ctx, resp, attempt)

	return nil, retry, statusErr
}

func (c *Client) buildRequest(ctx context.Context, fullURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, application/atom+xml;q=0.9")

	if c.authed {
		tok, tokErr := c.token(ctx, false)
		if tokErr != nil {
			return nil, fmt.Errorf("acquire token: %w", tokErr)
		}

		req.Header.Set("Authorization", "Bearer "+tok)
	}

	return req, nil
}

func (c *Client) handleNonSuccess(ctx context.Context, resp *http.Response, attempt int) (bool, error) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	_ = resp.Body.Close()

	apiErr := classify(resp.StatusCode, resp.Header.Get("Content-Type"), body, c.authed)

	if apiErr.Kind == KindUnauthorized && c.authed && attempt == 0 {
		c.logger.Debug("reddit 401, refreshing token")

		if _, err := c.token(ctx, true); err != nil {
			return false, fmt.Errorf("refresh token after 401: %w", err)
		}

		return true, apiErr
	}

	c.logger.Debug("reddit request failed",
		"status", apiErr.Status, "kind", apiErr.Kind, "attempt", attempt)

	return apiErr.Retryable(), apiErr
}

// readBody reads a successful response, rejecting payloads whose Content-Type
// does not match what the caller asked for — Reddit answers some blocks with a
// 200 HTML page.
func readBody(resp *http.Response, expect string) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, expect) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, errBodyLimit))

		return nil, &APIError{
			Status: resp.StatusCode,
			Kind:   KindBlocked,
			Detail: fmt.Sprintf("expected %s but got %s", expect, summarizeContentType(contentType)),
			Hint:   blockedHintAnon,
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	return body, nil
}

func (c *Client) sleepBackoff(ctx context.Context, attempt int) error {
	ms := min(backoffBaseMS*(1<<attempt), backoffCapMS)
	base := time.Duration(ms) * time.Millisecond
	delay := base + jitter20pct(base)

	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("ctx: %w", ctx.Err())
	}
}

func jitter20pct(base time.Duration) time.Duration {
	span := int64(base) * 2 / 5
	if span <= 0 {
		return 0
	}

	offset, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return 0
	}

	return time.Duration(offset.Int64()) - base/5
}

func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}

	if n, err := strconv.Atoi(strings.TrimSpace(header)); err == nil {
		return time.Duration(n) * time.Second
	}

	if t, err := http.ParseTime(header); err == nil {
		return time.Until(t)
	}

	return 0
}

func truncate(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + "..."
	}

	return s
}

func (c *Client) token(ctx context.Context, force bool) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if !force && c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-tokenLeeway)) {
		return c.accessToken, nil
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}

	req.SetBasicAuth(c.clientID, c.clientSecret)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
	if err != nil {
		return "", fmt.Errorf("read token body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", classify(resp.StatusCode, resp.Header.Get("Content-Type"), body, true)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}

	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}

	if tok.AccessToken == "" {
		return "", errors.New("token endpoint returned no access_token: " + tok.Error)
	}

	c.accessToken = tok.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	c.logger.Debug("reddit oauth token refreshed", "expires_in", tok.ExpiresIn)

	return c.accessToken, nil
}
