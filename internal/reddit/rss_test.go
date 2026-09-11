package reddit

import (
	"os"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return body
}

func TestParseRSSPosts(t *testing.T) {
	list, err := parseRSSPosts(readFixture(t, "post_feed.xml"), 0)
	if err != nil {
		t.Fatalf("parseRSSPosts: %v", err)
	}

	if list.DataSource != SourceRSS {
		t.Errorf("data source = %q, want %q", list.DataSource, SourceRSS)
	}

	if list.Note == "" {
		t.Error("rss results must carry a note explaining what is missing")
	}

	if len(list.Posts) != 2 {
		t.Fatalf("got %d posts, want 2", len(list.Posts))
	}

	self := list.Posts[0]
	if self.ID != "1wa14q6" {
		t.Errorf("id = %q, want 1wa14q6", self.ID)
	}

	if self.Subreddit != "golang" {
		t.Errorf("subreddit = %q, want golang", self.Subreddit)
	}

	if self.Author != "AutoModerator" {
		t.Errorf("author = %q, want AutoModerator", self.Author)
	}

	if !self.IsSelf {
		t.Error("a post whose [link] is its permalink is a self post")
	}

	// Entities are double-escaped in the feed; both rounds must come off.
	if !strings.Contains(self.Selftext, "we've got a dozen of those") {
		t.Errorf("selftext not unescaped: %q", self.Selftext)
	}

	if strings.Contains(self.Selftext, "submitted by") {
		t.Errorf("feed chrome leaked into selftext: %q", self.Selftext)
	}

	if self.CreatedUTC == 0 {
		t.Error("created_utc not parsed")
	}

	// The whole point of the pointer fields: RSS knows none of these.
	if self.Score != nil || self.NumComments != nil || self.UpvoteRatio != nil || self.Over18 != nil {
		t.Error("rss posts must leave unknown metrics absent, not zero")
	}

	link := list.Posts[1]
	if link.URL != "https://go.dev/blog/generics" {
		t.Errorf("link post url = %q, want the submitted link", link.URL)
	}

	if link.IsSelf {
		t.Error("a post linking elsewhere is not a self post")
	}
}

func TestParseRSSPostsTruncates(t *testing.T) {
	list, err := parseRSSPosts(readFixture(t, "post_feed.xml"), 20)
	if err != nil {
		t.Fatalf("parseRSSPosts: %v", err)
	}

	post := list.Posts[0]
	if !post.SelftextTruncated {
		t.Fatal("selftext should be marked truncated")
	}

	if len([]rune(post.Selftext)) > 21 {
		t.Errorf("selftext not truncated: %q", post.Selftext)
	}
}

func TestParseRSSCommentsFromThreadFeed(t *testing.T) {
	body := readFixture(t, "comments_feed.xml")

	posts, err := parseRSSPosts(body, 0)
	if err != nil {
		t.Fatalf("parseRSSPosts: %v", err)
	}

	// A thread feed leads with the post itself, then its comments.
	if len(posts.Posts) != 1 {
		t.Fatalf("got %d posts in thread feed, want 1", len(posts.Posts))
	}

	if posts.Posts[0].ID != "1wa14q6" {
		t.Errorf("post id = %q, want 1wa14q6", posts.Posts[0].ID)
	}

	comments, err := parseRSSComments(body, 0)
	if err != nil {
		t.Fatalf("parseRSSComments: %v", err)
	}

	if len(comments.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments.Comments))
	}

	first := comments.Comments[0]
	if first.ID == "" || strings.HasPrefix(first.ID, "t1_") {
		t.Errorf("comment id = %q, want a bare base36 id", first.ID)
	}

	if first.Score != nil {
		t.Error("rss comments have no score")
	}

	if first.Body == "" {
		t.Error("comment body not extracted")
	}
}

func TestParseRSSSubreddits(t *testing.T) {
	list, err := parseRSSSubreddits(readFixture(t, "subreddits_feed.xml"))
	if err != nil {
		t.Fatalf("parseRSSSubreddits: %v", err)
	}

	if len(list.Subreddits) != 2 {
		t.Fatalf("got %d subreddits, want 2", len(list.Subreddits))
	}

	// The feed title is the display title, so the addressable name has to come
	// from the link: "Ask Reddit..." is not a subreddit you can browse.
	if list.Subreddits[1].Name != "AskReddit" {
		t.Errorf("name = %q, want AskReddit", list.Subreddits[1].Name)
	}

	if list.Subreddits[1].Title != "Ask Reddit..." {
		t.Errorf("title = %q, want %q", list.Subreddits[1].Title, "Ask Reddit...")
	}

	desc := list.Subreddits[1].Description
	if !strings.Contains(desc, "thought-provoking questions") {
		t.Errorf("description = %q", desc)
	}

	if strings.Contains(desc, "[link]") {
		t.Errorf("feed chrome leaked into description: %q", desc)
	}

	if list.Subreddits[0].Subscribers != nil {
		t.Error("rss subreddits have no subscriber count")
	}
}

func TestCleanTextCollapsesMarkup(t *testing.T) {
	// cleanText sees content the XML decoder has already unescaped once, so
	// what reaches it is real markup plus one surviving layer of entities.
	got := cleanText(`<div class="md"><p>a &#39;b&#39;   c</p></div>`)
	if got != "a 'b' c" {
		t.Errorf("cleanText = %q, want %q", got, "a 'b' c")
	}
}
