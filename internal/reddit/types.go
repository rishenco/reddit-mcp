package reddit

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Results are rendered to text, not marshalled, so these types carry no JSON
// tags. What they do carry is the distinction between "zero" and "unknown":
// the RSS fallback genuinely does not know a post's score, and a plain int
// would report that as no upvotes.
const (
	SourceAPI = "api"
	SourceRSS = "rss"
)

// rssNote is deliberately terse. The full explanation of what RSS costs lives
// in the server instructions, which the client reads once, rather than in
// every listing the model reads.
const rssNote = "logged-out RSS fallback: no scores, counts, ratios or NSFW flags"

type Post struct {
	ID                string
	Subreddit         string
	Title             string
	Author            string
	URL               string
	Permalink         string
	Score             *int
	UpvoteRatio       *float64
	NumComments       *int
	CreatedUTC        int64
	IsSelf            bool
	Selftext          string
	SelftextTruncated bool
	Over18            *bool
	Stickied          bool
	Locked            bool
	LinkFlairText     string
}

// Comment is flattened: nested replies are separate entries carrying ParentID
// and Depth, so a deep thread renders as an indented list without recursion.
type Comment struct {
	ID            string
	ParentID      string
	Depth         int
	Subreddit     string
	Author        string
	Body          string
	BodyTruncated bool
	Score         *int
	CreatedUTC    int64
	Permalink     string
	IsSubmitter   bool
}

type User struct {
	Name              string
	ID                string
	CreatedUTC        int64
	LinkKarma         int
	CommentKarma      int
	TotalKarma        int
	IsMod             bool
	IsGold            bool
	IsEmployee        bool
	PublicDescription string
}

type Subreddit struct {
	Name          string
	Title         string
	Description   string
	Subscribers   *int
	ActiveUsers   *int
	CreatedUTC    int64
	Over18        *bool
	Lang          string
	URL           string
	SubredditType string
	Rules         []Rule
}

// Rule is one of a subreddit's posting rules. Worth reading before a human
// asks "why was this removed" or "can I post X here".
type Rule struct {
	Name            string
	Kind            string
	Description     string
	ViolationReason string
}

// WikiPage is a subreddit wiki page: FAQs, guides and community documentation
// that exist nowhere else in the API.
type WikiPage struct {
	Subreddit  string
	Page       string
	Content    string
	RevisedUTC int64
	RevisedBy  string
	URL        string
}

// source is embedded in every list so the renderer can say where the data came
// from and which fields to distrust.
type source struct {
	DataSource string
	Note       string
}

func apiSource() source { return source{DataSource: SourceAPI} }
func rssSource() source { return source{DataSource: SourceRSS, Note: rssNote} }

type PostList struct {
	source

	Posts []Post
	After string
}

type CommentList struct {
	source

	Comments []Comment
	After    string
	// MoreCount is how many replies Reddit collapsed behind "load more"
	// placeholders and this response therefore does not contain.
	MoreCount int
	// MoreParentIDs are the comments whose replies were collapsed. Pass one as
	// comment_id to get_post_comments to expand that subtree.
	MoreParentIDs []string
}

type SubredditList struct {
	source

	Subreddits []Subreddit
	After      string
}

type UserList struct {
	source

	Users []User
	After string
}

// maxMoreParents caps how many drill-in ids are reported, so a thread with
// hundreds of collapsed branches does not flood the response.
const maxMoreParents = 25

type rawListing struct {
	Data struct {
		After    string     `json:"after"`
		Children []rawChild `json:"children"`
	} `json:"data"`
}

type rawChild struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type rawMore struct {
	Count    int    `json:"count"`
	ParentID string `json:"parent_id"`
}

type rawPost struct {
	ID            string  `json:"id"`
	Subreddit     string  `json:"subreddit"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	URL           string  `json:"url"`
	Permalink     string  `json:"permalink"`
	Score         int     `json:"score"`
	UpvoteRatio   float64 `json:"upvote_ratio"`
	NumComments   int     `json:"num_comments"`
	CreatedUTC    float64 `json:"created_utc"`
	IsSelf        bool    `json:"is_self"`
	Selftext      string  `json:"selftext"`
	Over18        bool    `json:"over_18"`
	Stickied      bool    `json:"stickied"`
	Locked        bool    `json:"locked"`
	LinkFlairText string  `json:"link_flair_text"`
}

func (r rawPost) toPost(maxText int) Post {
	selftext, truncated := truncateText(r.Selftext, maxText)

	return Post{
		ID:                r.ID,
		Subreddit:         r.Subreddit,
		Title:             r.Title,
		Author:            r.Author,
		URL:               r.URL,
		Permalink:         absolutePermalink(r.Permalink),
		Score:             ptr(r.Score),
		UpvoteRatio:       ptr(r.UpvoteRatio),
		NumComments:       ptr(r.NumComments),
		CreatedUTC:        int64(r.CreatedUTC),
		IsSelf:            r.IsSelf,
		Selftext:          selftext,
		SelftextTruncated: truncated,
		Over18:            ptr(r.Over18),
		Stickied:          r.Stickied,
		Locked:            r.Locked,
		LinkFlairText:     r.LinkFlairText,
	}
}

type rawComment struct {
	ID          string          `json:"id"`
	Subreddit   string          `json:"subreddit"`
	Author      string          `json:"author"`
	Body        string          `json:"body"`
	Score       int             `json:"score"`
	CreatedUTC  float64         `json:"created_utc"`
	Permalink   string          `json:"permalink"`
	IsSubmitter bool            `json:"is_submitter"`
	Replies     json.RawMessage `json:"replies"`
}

// moreTracker accumulates the "load more" placeholders Reddit puts in a comment
// tree, so a truncated thread can say so instead of looking complete.
type moreTracker struct {
	count   int
	parents []string
}

func (m *moreTracker) add(more rawMore) {
	m.count += more.Count

	id := strings.TrimPrefix(more.ParentID, "t1_")
	if id == more.ParentID {
		return // t3_: more top-level comments, expanded with `after`, not `comment_id`
	}

	if len(m.parents) < maxMoreParents {
		m.parents = append(m.parents, id)
	}
}

func (r rawComment) flatten(parentID string, depth, maxText int, out *[]Comment, more *moreTracker) {
	body, truncated := truncateText(r.Body, maxText)

	*out = append(*out, Comment{
		ID:            r.ID,
		ParentID:      parentID,
		Depth:         depth,
		Subreddit:     r.Subreddit,
		Author:        r.Author,
		Body:          body,
		BodyTruncated: truncated,
		Score:         ptr(r.Score),
		CreatedUTC:    int64(r.CreatedUTC),
		Permalink:     absolutePermalink(r.Permalink),
		IsSubmitter:   r.IsSubmitter,
	})

	if len(r.Replies) == 0 {
		return
	}

	var empty string
	if json.Unmarshal(r.Replies, &empty) == nil {
		return
	}

	var listing rawListing
	if err := json.Unmarshal(r.Replies, &listing); err != nil {
		return
	}

	for _, ch := range listing.Data.Children {
		switch ch.Kind {
		case "t1":
			var child rawComment
			if err := json.Unmarshal(ch.Data, &child); err != nil {
				continue
			}

			child.flatten(r.ID, depth+1, maxText, out, more)
		case "more":
			var m rawMore
			if err := json.Unmarshal(ch.Data, &m); err == nil {
				more.add(m)
			}
		}
	}
}

type rawUser struct {
	Name         string  `json:"name"`
	ID           string  `json:"id"`
	CreatedUTC   float64 `json:"created_utc"`
	LinkKarma    int     `json:"link_karma"`
	CommentKarma int     `json:"comment_karma"`
	TotalKarma   int     `json:"total_karma"`
	IsMod        bool    `json:"is_mod"`
	IsGold       bool    `json:"is_gold"`
	IsEmployee   bool    `json:"is_employee"`
	Subreddit    struct {
		PublicDescription string `json:"public_description"`
	} `json:"subreddit"`
}

func (r rawUser) toUser() User {
	return User{
		Name:              r.Name,
		ID:                r.ID,
		CreatedUTC:        int64(r.CreatedUTC),
		LinkKarma:         r.LinkKarma,
		CommentKarma:      r.CommentKarma,
		TotalKarma:        r.TotalKarma,
		IsMod:             r.IsMod,
		IsGold:            r.IsGold,
		IsEmployee:        r.IsEmployee,
		PublicDescription: r.Subreddit.PublicDescription,
	}
}

type rawSubreddit struct {
	DisplayName       string  `json:"display_name"`
	Title             string  `json:"title"`
	PublicDescription string  `json:"public_description"`
	Subscribers       int     `json:"subscribers"`
	ActiveUserCount   int     `json:"active_user_count"`
	CreatedUTC        float64 `json:"created_utc"`
	Over18            bool    `json:"over18"`
	Lang              string  `json:"lang"`
	URL               string  `json:"url"`
	SubredditType     string  `json:"subreddit_type"`
}

func (r rawSubreddit) toSubreddit() Subreddit {
	return Subreddit{
		Name:          r.DisplayName,
		Title:         r.Title,
		Description:   r.PublicDescription,
		Subscribers:   ptr(r.Subscribers),
		ActiveUsers:   ptr(r.ActiveUserCount),
		CreatedUTC:    int64(r.CreatedUTC),
		Over18:        ptr(r.Over18),
		Lang:          r.Lang,
		URL:           absolutePermalink(r.URL),
		SubredditType: r.SubredditType,
	}
}

func decodePostListing(body json.RawMessage, maxText int) (*PostList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode post listing: %w", err)
	}

	out := &PostList{source: apiSource(), After: listing.Data.After}

	for _, ch := range listing.Data.Children {
		if ch.Kind != "t3" {
			continue
		}

		var rp rawPost
		if err := json.Unmarshal(ch.Data, &rp); err != nil {
			return nil, fmt.Errorf("decode post: %w", err)
		}

		out.Posts = append(out.Posts, rp.toPost(maxText))
	}

	return out, nil
}

func decodeCommentListing(body json.RawMessage, maxText int) (*CommentList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode comment listing: %w", err)
	}

	out := &CommentList{source: apiSource(), After: listing.Data.After}

	var more moreTracker

	for _, ch := range listing.Data.Children {
		switch ch.Kind {
		case "t1":
			var rc rawComment
			if err := json.Unmarshal(ch.Data, &rc); err != nil {
				return nil, fmt.Errorf("decode comment: %w", err)
			}

			rc.flatten("", 0, maxText, &out.Comments, &more)
		case "more":
			var m rawMore
			if err := json.Unmarshal(ch.Data, &m); err == nil {
				more.add(m)
			}
		}
	}

	out.MoreCount = more.count
	out.MoreParentIDs = more.parents

	return out, nil
}

func decodeSubredditListing(body json.RawMessage) (*SubredditList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode subreddit listing: %w", err)
	}

	out := &SubredditList{source: apiSource(), After: listing.Data.After}

	for _, ch := range listing.Data.Children {
		if ch.Kind != "t5" {
			continue
		}

		var rs rawSubreddit
		if err := json.Unmarshal(ch.Data, &rs); err != nil {
			return nil, fmt.Errorf("decode subreddit: %w", err)
		}

		out.Subreddits = append(out.Subreddits, rs.toSubreddit())
	}

	return out, nil
}

func decodeUserListing(body json.RawMessage) (*UserList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode user listing: %w", err)
	}

	out := &UserList{source: apiSource(), After: listing.Data.After}

	for _, ch := range listing.Data.Children {
		if ch.Kind != "t2" {
			continue
		}

		var ru rawUser
		if err := json.Unmarshal(ch.Data, &ru); err != nil {
			return nil, fmt.Errorf("decode user: %w", err)
		}

		out.Users = append(out.Users, ru.toUser())
	}

	return out, nil
}

func decodeRules(body json.RawMessage) ([]Rule, error) {
	var payload struct {
		Rules []struct {
			ShortName       string `json:"short_name"`
			Kind            string `json:"kind"`
			Description     string `json:"description"`
			ViolationReason string `json:"violation_reason"`
		} `json:"rules"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode rules: %w", err)
	}

	rules := make([]Rule, 0, len(payload.Rules))
	for _, r := range payload.Rules {
		rules = append(rules, Rule{
			Name:            r.ShortName,
			Kind:            r.Kind,
			Description:     r.Description,
			ViolationReason: r.ViolationReason,
		})
	}

	return rules, nil
}

func decodeWikiPage(body json.RawMessage, subreddit, page string) (*WikiPage, error) {
	var payload struct {
		Kind string `json:"kind"`
		Data struct {
			ContentMD    string  `json:"content_md"`
			RevisionDate float64 `json:"revision_date"`
			RevisionBy   struct {
				Data rawUser `json:"data"`
			} `json:"revision_by"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode wiki page: %w", err)
	}

	return &WikiPage{
		Subreddit:  subreddit,
		Page:       page,
		Content:    payload.Data.ContentMD,
		RevisedUTC: int64(payload.Data.RevisionDate),
		RevisedBy:  payload.Data.RevisionBy.Data.Name,
		URL:        anonHost + "/r/" + subreddit + "/wiki/" + page,
	}, nil
}

func absolutePermalink(permalink string) string {
	if permalink == "" {
		return ""
	}

	if strings.HasPrefix(permalink, "http") {
		return permalink
	}

	return anonHost + permalink
}

func ptr[T any](v T) *T { return &v }

// truncateText shortens body text to maxRunes runes, reporting whether it cut.
// Listings are the expensive case: 100 posts with full self-text can be several
// hundred kilobytes of context for a result the model is only skimming.
func truncateText(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s, false
	}

	count := 0
	for i := range s {
		if count == maxRunes {
			return strings.TrimRight(s[:i], " \n\t") + "…", true
		}

		count++
	}

	return s, false
}
