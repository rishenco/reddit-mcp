package reddit

import (
	"strings"
	"testing"
)

func TestNormalizePostID(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"bare id", "1wa14q6", "1wa14q6"},
		{"fullname", "t3_1wa14q6", "1wa14q6"},
		{"permalink", "https://www.reddit.com/r/golang/comments/1wa14q6/small_projects/", "1wa14q6"},
		{"permalink without scheme", "reddit.com/r/golang/comments/1wa14q6/small_projects/", "1wa14q6"},
		{"old reddit", "https://old.reddit.com/r/golang/comments/1wa14q6/small_projects/", "1wa14q6"},
		{"comment permalink", "https://www.reddit.com/r/golang/comments/1wa14q6/t/p8l0z29/", "1wa14q6"},
		{"short link", "https://redd.it/1wa14q6", "1wa14q6"},
		{"uppercase", "T3_1WA14Q6", "1wa14q6"},
		{"whitespace", "  1wa14q6  ", "1wa14q6"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizePostID(tc.input)
			if err != nil {
				t.Fatalf("normalizePostID(%q): %v", tc.input, err)
			}

			if got != tc.want {
				t.Errorf("normalizePostID(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizePostIDRejectsGarbage(t *testing.T) {
	for _, input := range []string{"", "   ", "https://example.com/not-reddit", "what is this"} {
		if _, err := normalizePostID(input); err == nil {
			t.Errorf("normalizePostID(%q) should fail", input)
		}
	}
}

func TestNormalizePostIDErrorExplainsAcceptedForms(t *testing.T) {
	_, err := normalizePostID("not a post")
	if err == nil {
		t.Fatal("expected an error")
	}

	for _, want := range []string{"base36", "permalink", "redd.it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestNormalizeCommentID(t *testing.T) {
	cases := map[string]string{
		"p8l0z29":     "p8l0z29",
		"t1_p8l0z29":  "p8l0z29",
		"https://www.reddit.com/r/golang/comments/1wa14q6/t/p8l0z29/": "p8l0z29",
	}

	for input, want := range cases {
		got, err := normalizeCommentID(input)
		if err != nil {
			t.Fatalf("normalizeCommentID(%q): %v", input, err)
		}

		if got != want {
			t.Errorf("normalizeCommentID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSubredditAndUsername(t *testing.T) {
	for input, want := range map[string]string{"golang": "golang", "r/golang": "golang", "/r/golang/": "golang"} {
		if got := normalizeSubreddit(input); got != want {
			t.Errorf("normalizeSubreddit(%q) = %q, want %q", input, got, want)
		}
	}

	for input, want := range map[string]string{"spez": "spez", "u/spez": "spez", "/user/spez/": "spez"} {
		if got := normalizeUsername(input); got != want {
			t.Errorf("normalizeUsername(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClampLimitAndDepth(t *testing.T) {
	for input, want := range map[int]int{0: defaultLimit, -5: defaultLimit, 10: 10, 500: maxLimit} {
		if got := clampLimit(input); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", input, got, want)
		}
	}

	// The schema promises 1-10; a model passing 50 must not reach Reddit with it.
	for input, want := range map[int]int{0: 0, -1: 0, 5: 5, 50: maxDepth} {
		if got := clampDepth(input); got != want {
			t.Errorf("clampDepth(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestValidateRejectsUnknownSort(t *testing.T) {
	err := validate("sort", "sideways", postSorts)
	if err == nil {
		t.Fatal("an unknown sort should be reported, not silently ignored by Reddit")
	}

	if !strings.Contains(err.Error(), "controversial") {
		t.Errorf("error should list the accepted values, got: %v", err)
	}

	if err := validate("sort", "top", postSorts); err != nil {
		t.Errorf("top is a valid sort: %v", err)
	}
}

func TestDecodeCommentListingReportsCollapsedReplies(t *testing.T) {
	body := []byte(`{"data":{"after":"","children":[
		{"kind":"t1","data":{"id":"a1","body":"top","score":5,"replies":{"data":{"children":[
			{"kind":"t1","data":{"id":"b1","body":"child","score":2,"replies":""}},
			{"kind":"more","data":{"count":7,"parent_id":"t1_a1"}}
		]}}}},
		{"kind":"more","data":{"count":40,"parent_id":"t3_zzz"}}
	]}}`)

	list, err := decodeCommentListing(body, 0)
	if err != nil {
		t.Fatalf("decodeCommentListing: %v", err)
	}

	if len(list.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(list.Comments))
	}

	if list.Comments[1].Depth != 1 || list.Comments[1].ParentID != "a1" {
		t.Errorf("nested comment lost its position: %+v", list.Comments[1])
	}

	// Silently dropping these is what made truncated threads look complete.
	if list.MoreCount != 47 {
		t.Errorf("more_count = %d, want 47", list.MoreCount)
	}

	// t3_ parents mean "more top-level comments", which comment_id cannot expand.
	if len(list.MoreParentIDs) != 1 || list.MoreParentIDs[0] != "a1" {
		t.Errorf("more_parent_ids = %v, want [a1]", list.MoreParentIDs)
	}
}

func TestDecodePostListingTruncatesSelftext(t *testing.T) {
	body := []byte(`{"data":{"after":"t3_x","children":[
		{"kind":"t3","data":{"id":"a","title":"T","selftext":"` + strings.Repeat("x", 100) + `","score":3}}
	]}}`)

	list, err := decodePostListing(body, 10)
	if err != nil {
		t.Fatalf("decodePostListing: %v", err)
	}

	post := list.Posts[0]
	if !post.SelftextTruncated {
		t.Error("selftext should be flagged as truncated")
	}

	if len([]rune(post.Selftext)) > 11 {
		t.Errorf("selftext = %d runes, want ~10", len([]rune(post.Selftext)))
	}

	if post.Score == nil || *post.Score != 3 {
		t.Error("api results must keep their real score")
	}

	if list.DataSource != SourceAPI {
		t.Errorf("data source = %q, want %q", list.DataSource, SourceAPI)
	}

	// Full-text reads pass 0 and must come back whole.
	full, err := decodePostListing(body, 0)
	if err != nil {
		t.Fatalf("decodePostListing: %v", err)
	}

	if full.Posts[0].SelftextTruncated || len(full.Posts[0].Selftext) != 100 {
		t.Error("maxText 0 must disable truncation")
	}
}

func TestTruncateTextIsRuneSafe(t *testing.T) {
	got, truncated := truncateText("привет мир", 6)
	if !truncated {
		t.Fatal("expected truncation")
	}

	if !strings.HasPrefix(got, "привет") {
		t.Errorf("truncateText split a multi-byte rune: %q", got)
	}

	if _, truncated := truncateText("short", 100); truncated {
		t.Error("short text should not be marked truncated")
	}
}

func TestRSSParamsOmitCursor(t *testing.T) {
	// Atom feeds have no `after`, so sending one would silently do nothing.
	params := rssParams(10, "top", "week")
	if params.Get("limit") != "10" || params.Get("t") != "week" {
		t.Errorf("rssParams = %v", params)
	}

	if params.Has("after") {
		t.Error("rss feeds are not paginated")
	}
}
