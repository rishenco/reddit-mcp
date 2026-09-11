package tools_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rishenco/reddit-mcp/internal/reddit"
	"github.com/rishenco/reddit-mcp/internal/tools"
)

const listingJSON = `{"data":{"after":"t3_next","children":[
	{"kind":"t3","data":{"id":"1wa14q6","title":"Small Projects","subreddit":"golang",
	 "author":"AutoModerator","score":42,"upvote_ratio":0.97,"num_comments":13,
	 "permalink":"/r/golang/comments/1wa14q6/small_projects/","created_utc":1757271698,
	 "is_self":true,"selftext":"This is the weekly thread."}}
]}}`

// recorder is a stub Reddit that remembers the paths it was asked for.
type recorder struct {
	mu    sync.Mutex
	paths []string
	body  func(path string) string
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.paths = append(r.paths, req.URL.Path)
	r.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(r.body(req.URL.Path)))
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.paths...)
}

// connect wires an MCP client to a server backed by a stub Reddit.
func connect(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()

	backend := httptest.NewServer(handler)
	t.Cleanup(backend.Close)

	client := reddit.New(reddit.Options{
		BaseURL:      backend.URL,
		RSSBaseURL:   backend.URL,
		RateLimitRPM: 60000,
	})

	server := mcp.NewServer(
		&mcp.Implementation{Name: "reddit-mcp", Version: "test"},
		&mcp.ServerOptions{Instructions: tools.Instructions},
	)
	tools.Register(server, client)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	t.Cleanup(func() { _ = serverSession.Close() })

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() { _ = session.Close() })

	return session
}

func okJSON(body string) http.Handler {
	return &recorder{body: func(string) string { return body }}
}

// call runs a tool and fails the test if it reported an error.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}

	body := resultText(t, res)
	if res.IsError {
		t.Fatalf("%s reported an error: %s", name, body)
	}

	return body
}

func callErr(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}

	if !res.IsError {
		t.Fatalf("%s should have failed, got: %s", name, resultText(t, res))
	}

	return resultText(t, res)
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()

	if len(res.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(res.Content))
	}

	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}

	return text.Text
}

func TestToolsAreReadOnlyAndCarryNoOutputSchema(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(res.Tools) != 12 {
		t.Errorf("got %d tools, want 12", len(res.Tools))
	}

	for _, tool := range res.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only; clients cannot auto-approve it", tool.Name)
		}

		if tool.Annotations != nil &&
			(tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint) {
			t.Errorf("%s talks to Reddit and should be marked open-world", tool.Name)
		}

		if tool.Annotations != nil && tool.Annotations.Title == "" {
			t.Errorf("%s has no display title", tool.Name)
		}

		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}

		// Output schemas were 57% of this listing and nothing reads the
		// structured half back, so they are deliberately not declared.
		if tool.OutputSchema != nil {
			t.Errorf("%s declares an output schema; results are text-only", tool.Name)
		}
	}
}

func TestResultsAreTextOnly(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "browse_subreddit",
		Arguments: map[string]any{"subreddit": "golang"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if res.StructuredContent != nil {
		t.Error("a structured copy of the payload doubles the response for no reader")
	}

	body := resultText(t, res)
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Error("the content block is raw JSON rather than a rendering")
	}

	for _, want := range []string{"r/golang", "Small Projects", "42 pts", "97% up", "13 comments",
		"id=1wa14q6", "after=t3_next"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendering is missing %q:\n%s", want, body)
		}
	}

	if len(body) >= len(listingJSON) {
		t.Errorf("rendering (%d bytes) is not smaller than the raw JSON (%d bytes)",
			len(body), len(listingJSON))
	}
}

func TestBrowseCombinesSubredditsIntoOneRequest(t *testing.T) {
	stub := &recorder{body: func(string) string { return listingJSON }}
	session := connect(t, stub)

	call(t, session, "browse_subreddit", map[string]any{"subreddit": "golang, rust", "sort": "new"})

	paths := stub.seen()
	if len(paths) != 1 {
		t.Fatalf("made %d requests, want 1", len(paths))
	}

	if paths[0] != "/r/golang+rust/new.json" {
		t.Errorf("path = %q, want the subreddits combined", paths[0])
	}
}

func TestFindDiscussionsRoutesByArgument(t *testing.T) {
	duplicates := `[{"data":{"children":[]}},` + listingJSON + `]`

	stub := &recorder{body: func(path string) string {
		if strings.HasPrefix(path, "/duplicates/") {
			return duplicates
		}

		return listingJSON
	}}
	session := connect(t, stub)

	call(t, session, "find_discussions", map[string]any{"url": "https://go.dev/blog/generics"})
	call(t, session, "find_discussions", map[string]any{"post_id": "1wa14q6"})

	paths := stub.seen()
	if len(paths) != 2 {
		t.Fatalf("made %d requests, want 2", len(paths))
	}

	if !strings.HasPrefix(paths[0], "/api/info") {
		t.Errorf("a url should be looked up through /api/info, got %q", paths[0])
	}

	if !strings.HasPrefix(paths[1], "/duplicates/1wa14q6") {
		t.Errorf("a post id should be looked up through /duplicates, got %q", paths[1])
	}

	if msg := callErr(t, session, "find_discussions", map[string]any{}); !strings.Contains(msg, "post_id") {
		t.Errorf("calling with neither argument should say what is needed, got: %s", msg)
	}
}

func TestSubredditRulesAreOptional(t *testing.T) {
	about := `{"kind":"t5","data":{"display_name":"golang","title":"Go","subscribers":300000,
		"active_user_count":400,"public_description":"Go talk","created_utc":1200000000}}`
	rules := `{"rules":[{"short_name":"Be civil","kind":"all","description":"No personal attacks."}]}`

	stub := &recorder{body: func(path string) string {
		if strings.Contains(path, "/about/rules") {
			return rules
		}

		return about
	}}
	session := connect(t, stub)

	plain := call(t, session, "get_subreddit_info", map[string]any{"subreddit": "golang"})
	if strings.Contains(plain, "Be civil") {
		t.Error("rules should not be fetched unless asked for")
	}

	if len(stub.seen()) != 1 {
		t.Errorf("made %d requests without rules, want 1", len(stub.seen()))
	}

	withRules := call(t, session, "get_subreddit_info",
		map[string]any{"subreddit": "golang", "include_rules": true})

	for _, want := range []string{"Be civil", "No personal attacks", "300000 subscribers"} {
		if !strings.Contains(withRules, want) {
			t.Errorf("rendering is missing %q:\n%s", want, withRules)
		}
	}
}

func TestWikiPageIsRendered(t *testing.T) {
	wiki := `{"kind":"wikipage","data":{"content_md":"# FAQ\n\nRead the docs.",
		"revision_date":1700000000,"revision_by":{"data":{"name":"mod1"}}}}`

	session := connect(t, &recorder{body: func(string) string { return wiki }})

	body := call(t, session, "get_wiki_page", map[string]any{"subreddit": "golang", "page": "faq"})

	for _, want := range []string{"r/golang wiki: faq", "Read the docs", "u/mod1"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendering is missing %q:\n%s", want, body)
		}
	}
}

func TestBrowseCommentsPicksTheRightStream(t *testing.T) {
	comments := `{"data":{"after":"","children":[
		{"kind":"t1","data":{"id":"c1","author":"gopher","body":"nice","score":4,
		 "subreddit":"golang","permalink":"/r/golang/comments/x/y/c1/","replies":""}}
	]}}`

	stub := &recorder{body: func(string) string { return comments }}
	session := connect(t, stub)

	body := call(t, session, "browse_comments", map[string]any{"subreddit": "golang"})
	if !strings.Contains(body, "u/gopher") || !strings.Contains(body, "4 pts") {
		t.Errorf("comment not rendered:\n%s", body)
	}

	call(t, session, "browse_comments", map[string]any{"username": "spez"})

	paths := stub.seen()
	if paths[0] != "/r/golang/comments.json" {
		t.Errorf("subreddit stream path = %q", paths[0])
	}

	if paths[1] != "/user/spez/comments.json" {
		t.Errorf("user stream path = %q", paths[1])
	}

	if msg := callErr(t, session, "browse_comments",
		map[string]any{"subreddit": "golang", "username": "spez"}); !strings.Contains(msg, "not both") {
		t.Errorf("both arguments at once should be refused, got: %s", msg)
	}
}

func TestSearchUsersIsRendered(t *testing.T) {
	users := `{"data":{"after":"","children":[
		{"kind":"t2","data":{"name":"spez","link_karma":1000,"comment_karma":2000,
		 "created_utc":1130000000,"is_employee":true}}
	]}}`

	session := connect(t, &recorder{body: func(string) string { return users }})

	body := call(t, session, "search_users", map[string]any{"query": "spez"})

	for _, want := range []string{"u/spez", "1000 post karma", "reddit employee"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendering is missing %q:\n%s", want, body)
		}
	}
}

func TestServerSendsInstructions(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	got := session.InitializeResult().Instructions
	if got == "" {
		t.Fatal("the server should tell the client how to use the toolset")
	}

	for _, want := range []string{"rss", "more replies not loaded", "get_posts", "find_discussions"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions should mention %q", want)
		}
	}
}

func TestBlockedCallReturnsActionableError(t *testing.T) {
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<body class=theme-beta><style>:root{--rem360:22.5rem;}</style>`))
	}))

	// get_user has no RSS fallback, so this surfaces the block itself.
	msg := callErr(t, session, "get_user", map[string]any{"username": "spez"})

	if !strings.Contains(msg, "REDDIT_CLIENT_ID") {
		t.Errorf("the model cannot act on this error: %s", msg)
	}

	if strings.Contains(msg, "--rem360") {
		t.Errorf("reddit's html block page leaked into the tool result: %s", msg)
	}
}

func TestInvalidSortIsRejectedWithChoices(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	msg := callErr(t, session, "browse_subreddit",
		map[string]any{"subreddit": "golang", "sort": "sideways"})

	if !strings.Contains(msg, "controversial") {
		t.Errorf("error should list valid sorts: %s", msg)
	}
}

func TestPostIDAcceptsAPermalink(t *testing.T) {
	stub := &recorder{body: func(string) string { return listingJSON }}
	session := connect(t, stub)

	call(t, session, "get_posts", map[string]any{
		"post_ids": []any{"https://www.reddit.com/r/golang/comments/1wa14q6/small_projects/"},
	})

	if !strings.Contains(stub.seen()[0], "t3_1wa14q6") {
		t.Errorf("path = %q, want the id pulled out of the permalink", stub.seen()[0])
	}
}
