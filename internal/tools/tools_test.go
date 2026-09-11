package tools_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// connect wires an MCP client to a server backed by a stub Reddit.
func connect(t *testing.T, handler http.HandlerFunc) *mcp.ClientSession {
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

func okJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func TestEveryToolIsMarkedReadOnly(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(res.Tools) != 12 {
		t.Errorf("got %d tools, want 12", len(res.Tools))
	}

	for _, tool := range res.Tools {
		if tool.Annotations == nil {
			t.Errorf("%s has no annotations; clients cannot auto-approve it", tool.Name)

			continue
		}

		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}

		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Errorf("%s talks to Reddit and should be marked open-world", tool.Name)
		}

		if tool.Annotations.Title == "" {
			t.Errorf("%s has no display title", tool.Name)
		}

		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
}

func TestServerSendsInstructions(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	got := session.InitializeResult().Instructions
	if got == "" {
		t.Fatal("the server should tell the client how to use the toolset")
	}

	for _, want := range []string{"data_source", "more_count", "get_posts"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions should mention %q", want)
		}
	}
}

func TestCallReturnsCompactTextAndStructuredData(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "browse_subreddit",
		Arguments: map[string]any{"subreddit": "golang"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if res.IsError {
		t.Fatalf("tool reported an error: %+v", res.Content)
	}

	if len(res.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(res.Content))
	}

	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}

	// The SDK repeats the JSON payload as text unless the handler fills Content
	// itself. That doubling is exactly what this rendering replaces.
	if strings.HasPrefix(strings.TrimSpace(text.Text), "{") {
		t.Error("content block is raw JSON; the payload is being sent twice")
	}

	for _, want := range []string{"r/golang", "Small Projects", "42 pts", "1wa14q6", "after=t3_next"} {
		if !strings.Contains(text.Text, want) {
			t.Errorf("rendering is missing %q:\n%s", want, text.Text)
		}
	}

	if len(text.Text) >= len(listingJSON) {
		t.Errorf("rendering (%d bytes) is not smaller than the raw JSON (%d bytes)",
			len(text.Text), len(listingJSON))
	}

	// The machine-readable half must survive.
	var payload struct {
		Posts []struct {
			ID    string `json:"id"`
			Score *int   `json:"score"`
		} `json:"posts"`
		DataSource string `json:"data_source"`
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}

	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("structured content: %v", err)
	}

	if len(payload.Posts) != 1 || payload.Posts[0].ID != "1wa14q6" {
		t.Errorf("structured content lost the posts: %+v", payload)
	}

	if payload.DataSource != reddit.SourceAPI {
		t.Errorf("data_source = %q, want %q", payload.DataSource, reddit.SourceAPI)
	}
}

func TestBlockedCallReturnsActionableError(t *testing.T) {
	session := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<body class=theme-beta><style>:root{--rem360:22.5rem;}</style>`))
	})

	// get_user has no RSS fallback, so this surfaces the block itself.
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_user",
		Arguments: map[string]any{"username": "spez"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if !res.IsError {
		t.Fatal("a blocked request should be reported as an error")
	}

	text, _ := res.Content[0].(*mcp.TextContent)
	if !strings.Contains(text.Text, "REDDIT_CLIENT_ID") {
		t.Errorf("the model cannot act on this error: %s", text.Text)
	}

	if strings.Contains(text.Text, "--rem360") {
		t.Errorf("reddit's html block page leaked into the tool result: %s", text.Text)
	}
}

func TestInvalidSortIsRejectedWithChoices(t *testing.T) {
	session := connect(t, okJSON(listingJSON))

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "browse_subreddit",
		Arguments: map[string]any{"subreddit": "golang", "sort": "sideways"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if !res.IsError {
		t.Fatal("an unknown sort should fail loudly, not silently return hot")
	}

	text, _ := res.Content[0].(*mcp.TextContent)
	if !strings.Contains(text.Text, "controversial") {
		t.Errorf("error should list valid sorts: %s", text.Text)
	}
}

func TestPostIDAcceptsAPermalink(t *testing.T) {
	var gotPath string

	session := connect(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listingJSON))
	})

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_post",
		Arguments: map[string]any{
			"post_id": "https://www.reddit.com/r/golang/comments/1wa14q6/small_projects/",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if res.IsError {
		text, _ := res.Content[0].(*mcp.TextContent)
		t.Fatalf("a permalink should be accepted: %s", text.Text)
	}

	if !strings.Contains(gotPath, "t3_1wa14q6") {
		t.Errorf("path = %q, want the id pulled out of the permalink", gotPath)
	}
}
