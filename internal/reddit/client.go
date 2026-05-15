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
	"time"
)

const (
	anonHost      = "https://www.reddit.com"
	authHost      = "https://oauth.reddit.com"
	oauthEndpoint = "https://www.reddit.com/api/v1/access_token"
	maxRetries    = 3

	httpTimeout      = 30 * time.Second
	tokenLeeway      = 10 * time.Second
	backoffBaseMS    = 100
	backoffCapMS     = 30000
	defaultRPMAnon   = 10
	defaultRPMAuthed = 60
	errBodyLimit     = 1024
	htmlBodyLimit    = 512
	errMsgLimit      = 200
)

type Client struct {
	httpClient   *http.Client
	logger       *slog.Logger
	userAgent    string
	clientID     string
	clientSecret string
	baseURL      string
	authed       bool

	limiter *limiter

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func New(clientID, clientSecret, userAgent string, rateLimitRPM int, logger *slog.Logger) *Client {
	authed := clientID != "" && clientSecret != ""

	rpm := rateLimitRPM
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

	return &Client{
		httpClient:   &http.Client{Timeout: httpTimeout},
		logger:       logger,
		userAgent:    userAgent,
		clientID:     clientID,
		clientSecret: clientSecret,
		baseURL:      baseURL,
		authed:       authed,
		limiter:      newLimiter(rpm, logger),
	}
}

func (c *Client) Authenticated() bool { return c.authed }

func (c *Client) get(ctx context.Context, path string, params url.Values) (json.RawMessage, error) {
	if params == nil {
		params = url.Values{}
	}

	params.Set("raw_json", "1")

	if !c.authed && !strings.HasSuffix(path, ".json") {
		path += ".json"
	}

	fullURL := c.baseURL + path + "?" + params.Encode()

	c.logger.Debug("reddit GET", "url", fullURL)

	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.limiter.wait(ctx); err != nil {
			return nil, fmt.Errorf("rate-limit wait: %w", err)
		}

		req, err := c.buildRequest(ctx, fullURL)
		if err != nil {
			return nil, err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("http do: %w", err)
			c.logger.Debug("reddit transport error", "attempt", attempt, "err", err)

			if berr := c.sleepBackoff(ctx, attempt); berr != nil {
				return nil, fmt.Errorf("backoff: %w", berr)
			}

			continue
		}

		c.limiter.observe(resp.Header, parseRetryAfter(resp.Header.Get("Retry-After")))

		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			body, readErr := readJSONBody(resp)
			if readErr != nil {
				return nil, fmt.Errorf("read body: %w", readErr)
			}

			return body, nil
		}

		retry, statusErr := c.handleNonSuccess(ctx, resp, attempt)
		lastErr = statusErr

		if !retry {
			return nil, lastErr
		}

		if resp.StatusCode >= http.StatusInternalServerError {
			if berr := c.sleepBackoff(ctx, attempt); berr != nil {
				return nil, fmt.Errorf("backoff: %w", berr)
			}
		}
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (c *Client) buildRequest(ctx context.Context, fullURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

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

	apiErr := &APIError{Status: resp.StatusCode, Body: string(body)}

	if resp.StatusCode == http.StatusUnauthorized && c.authed && attempt == 0 {
		c.logger.Debug("reddit 401, refreshing token")

		if _, err := c.token(ctx, true); err != nil {
			return false, fmt.Errorf("refresh token after 401: %w", err)
		}

		return true, apiErr
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		c.logger.Debug("reddit 429, deferring to limiter barrier", "attempt", attempt)

		return true, apiErr
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		c.logger.Debug("reddit 5xx", "status", resp.StatusCode, "attempt", attempt)

		return true, apiErr
	}

	return false, apiErr
}

func readJSONBody(resp *http.Response) (json.RawMessage, error) {
	defer func() { _ = resp.Body.Close() }()

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "json") {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, htmlBodyLimit))

		return nil, fmt.Errorf("unexpected content-type %q (got HTML or similar instead of JSON): %s",
			contentType, truncate(string(body), htmlBodyLimit/2))
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

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("reddit api: status %d: %s", e.Status, truncate(e.Body, errMsgLimit))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint status %d: %s",
			resp.StatusCode, truncate(string(body), errMsgLimit))
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
