package reddit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testClient points a client at a stub Reddit. The rate limit is lifted so the
// limiter does not turn a four-request test into a half-minute one.
func testClient(t *testing.T, handler http.HandlerFunc, opts Options) (*Client, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	if opts.RateLimitRPM == 0 {
		opts.RateLimitRPM = 60000
	}

	opts.BaseURL = srv.URL
	opts.RSSBaseURL = srv.URL

	return New(opts), srv
}

func TestBrowseSubredditFallsBackToRSS(t *testing.T) {
	var jsonHits, rssHits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".rss") {
			rssHits.Add(1)
			w.Header().Set("Content-Type", "application/atom+xml; charset=UTF-8")
			body, _ := os.ReadFile("testdata/post_feed.xml")
			_, _ = w.Write(body)

			return
		}

		jsonHits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(blockPage))
	}, Options{})

	list, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 2)
	if err != nil {
		t.Fatalf("BrowseSubreddit: %v", err)
	}

	if list.DataSource != SourceRSS {
		t.Errorf("data source = %q, want %q", list.DataSource, SourceRSS)
	}

	if len(list.Posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(list.Posts))
	}

	if !client.JSONBlocked() {
		t.Error("the client should remember that the Data API is blocked")
	}

	// A second call must not spend another request rediscovering the 403.
	if _, err := client.BrowseSubreddit(t.Context(), "golang", "new", "", "", 2); err != nil {
		t.Fatalf("second BrowseSubreddit: %v", err)
	}

	if got := jsonHits.Load(); got != 1 {
		t.Errorf("json endpoint hit %d times, want 1: the block should latch", got)
	}

	if got := rssHits.Load(); got != 2 {
		t.Errorf("rss endpoint hit %d times, want 2", got)
	}
}

func TestPrivateSubredditDoesNotFallBackToRSS(t *testing.T) {
	var rssHits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".rss") {
			rssHits.Add(1)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"reason":"private","message":"Forbidden"}`))
	}, Options{})

	_, err := client.BrowseSubreddit(t.Context(), "secret", "hot", "", "", 2)
	if err == nil {
		t.Fatal("expected an error for a private subreddit")
	}

	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error should name the reason, got: %v", err)
	}

	if rssHits.Load() != 0 {
		t.Error("RSS cannot read a private subreddit either; falling back just wastes a request")
	}
}

func TestResponsesAreCached(t *testing.T) {
	var hits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"after":"","children":[]}}`))
	}, Options{CacheTTL: time.Minute, CacheMaxBytes: 1 << 20})

	for range 3 {
		if _, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 5); err != nil {
			t.Fatalf("BrowseSubreddit: %v", err)
		}
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("reddit hit %d times, want 1", got)
	}

	cacheHits, _, _ := client.CacheStats()
	if cacheHits != 2 {
		t.Errorf("cache hits = %d, want 2", cacheHits)
	}
}

func TestCachingDisabledByDefault(t *testing.T) {
	var hits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"after":"","children":[]}}`))
	}, Options{})

	for range 2 {
		if _, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 5); err != nil {
			t.Fatalf("BrowseSubreddit: %v", err)
		}
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("reddit hit %d times, want 2 with caching off", got)
	}
}

func TestRetriesOn429(t *testing.T) {
	var hits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"after":"","children":[]}}`))
	}, Options{})

	if _, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 5); err != nil {
		t.Fatalf("BrowseSubreddit: %v", err)
	}

	if got := hits.Load(); got != 2 {
		t.Errorf("reddit hit %d times, want 2 (one 429 then a retry)", got)
	}
}

func TestNotFoundIsNotRetried(t *testing.T) {
	var hits atomic.Int32

	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found","error":404}`))
	}, Options{})

	_, err := client.BrowseSubreddit(t.Context(), "nope", "hot", "", "", 5)
	if err == nil {
		t.Fatal("expected an error")
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("reddit hit %d times, want 1: a 404 will not become a 200", got)
	}
}

func TestHTMLWithStatus200IsRejected(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(blockPage))
	}, Options{})

	_, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 5)
	if err == nil {
		t.Fatal("an HTML body with a 200 status is still not a listing")
	}

	if strings.Contains(err.Error(), "theme-beta") {
		t.Errorf("html leaked into the error: %v", err)
	}
}

func TestGetPostsUsesOneRequest(t *testing.T) {
	var paths []string

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"after":"","children":[
			{"kind":"t3","data":{"id":"aaa","title":"A","subreddit":"golang"}},
			{"kind":"t3","data":{"id":"bbb","title":"B","subreddit":"golang"}}
		]}}`))
	}, Options{})

	list, err := client.GetPosts(t.Context(), []string{
		"aaa",
		"https://www.reddit.com/r/golang/comments/bbb/title/",
	})
	if err != nil {
		t.Fatalf("GetPosts: %v", err)
	}

	if len(paths) != 1 {
		t.Fatalf("made %d requests, want 1", len(paths))
	}

	if !strings.Contains(paths[0], "t3_aaa,t3_bbb") {
		t.Errorf("path = %q, want both fullnames in one /by_id call", paths[0])
	}

	if len(list.Posts) != 2 {
		t.Errorf("got %d posts, want 2", len(list.Posts))
	}
}

func TestUserAgentIsSent(t *testing.T) {
	var got string

	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"after":"","children":[]}}`))
	}, Options{UserAgent: "go:reddit-mcp:v9.9.9"})

	if _, err := client.BrowseSubreddit(t.Context(), "golang", "hot", "", "", 5); err != nil {
		t.Fatalf("BrowseSubreddit: %v", err)
	}

	if got != "go:reddit-mcp:v9.9.9" {
		t.Errorf("User-Agent = %q", got)
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
	}, Options{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := client.BrowseSubreddit(ctx, "golang", "hot", "", "", 5); err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
}
