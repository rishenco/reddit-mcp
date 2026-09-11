package reddit

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// DataSource records where a result came from. Anything but "api" is degraded:
// the public RSS feeds carry no scores, comment counts or NSFW flags, and a
// model that cannot see the difference will happily report a score of zero.
const (
	SourceAPI = "api"
	SourceRSS = "rss"
)

const rssNote = "Served from Reddit's public RSS feed because logged-out Data API access is blocked. " +
	"Scores, comment counts, ratios and NSFW flags are unavailable; set REDDIT_CLIENT_ID and " +
	"REDDIT_CLIENT_SECRET for complete data."

type Post struct {
	ID        string `json:"id"`
	Subreddit string `json:"subreddit"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	URL       string `json:"url"`
	Permalink string `json:"permalink"`
	// Score, UpvoteRatio, NumComments and Over18 are pointers because the RSS
	// fallback genuinely does not know them. Absent is honest; zero is a lie.
	Score             *int     `json:"score,omitempty"`
	UpvoteRatio       *float64 `json:"upvote_ratio,omitempty"`
	NumComments       *int     `json:"num_comments,omitempty"`
	CreatedUTC        int64    `json:"created_utc"`
	IsSelf            bool     `json:"is_self,omitempty"`
	Selftext          string   `json:"selftext,omitempty"`
	SelftextTruncated bool     `json:"selftext_truncated,omitempty"`
	Over18            *bool    `json:"over_18,omitempty"`
	Stickied          bool     `json:"stickied,omitempty"`
	Locked            bool     `json:"locked,omitempty"`
	LinkFlairText     string   `json:"link_flair_text,omitempty"`
}

// Comment is flattened: nested replies are emitted as separate entries
// with ParentID and Depth set, to keep the output schema non-recursive.
type Comment struct {
	ID            string `json:"id"`
	ParentID      string `json:"parent_id,omitempty"`
	Depth         int    `json:"depth"`
	Author        string `json:"author"`
	Body          string `json:"body"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
	Score         *int   `json:"score,omitempty"`
	CreatedUTC    int64  `json:"created_utc"`
	Permalink     string `json:"permalink"`
	IsSubmitter   bool   `json:"is_submitter,omitempty"`
}

type User struct {
	Name              string `json:"name"`
	ID                string `json:"id"`
	CreatedUTC        int64  `json:"created_utc"`
	LinkKarma         int    `json:"link_karma"`
	CommentKarma      int    `json:"comment_karma"`
	TotalKarma        int    `json:"total_karma"`
	IsMod             bool   `json:"is_mod"`
	IsGold            bool   `json:"is_gold"`
	IsEmployee        bool   `json:"is_employee"`
	PublicDescription string `json:"public_description,omitempty"`
}

type Subreddit struct {
	Name          string `json:"name"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"public_description,omitempty"`
	Subscribers   *int   `json:"subscribers,omitempty"`
	ActiveUsers   *int   `json:"active_user_count,omitempty"`
	CreatedUTC    int64  `json:"created_utc,omitempty"`
	Over18        *bool  `json:"over_18,omitempty"`
	Lang          string `json:"lang,omitempty"`
	URL           string `json:"url"`
	SubredditType string `json:"subreddit_type,omitempty"`
}

type PostList struct {
	Posts      []Post `json:"posts"`
	After      string `json:"after,omitempty"`
	DataSource string `json:"data_source"`
	Note       string `json:"note,omitempty"`
}

type CommentList struct {
	Comments []Comment `json:"comments"`
	After    string    `json:"after,omitempty"`
	// MoreCount is how many replies Reddit collapsed behind "load more"
	// placeholders and this response therefore does not contain.
	MoreCount int `json:"more_count,omitempty"`
	// MoreParentIDs are the comments whose replies were collapsed. Pass one as
	// comment_id to get_post_comments to expand that subtree.
	MoreParentIDs []string `json:"more_parent_ids,omitempty"`
	DataSource    string   `json:"data_source"`
	Note          string   `json:"note,omitempty"`
}

type SubredditList struct {
	Subreddits []Subreddit `json:"subreddits"`
	After      string      `json:"after,omitempty"`
	DataSource string      `json:"data_source"`
	Note       string      `json:"note,omitempty"`
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

	out := &PostList{After: listing.Data.After, DataSource: SourceAPI, Posts: []Post{}}

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

	out := &CommentList{
		Comments:   make([]Comment, 0, len(listing.Data.Children)),
		After:      listing.Data.After,
		DataSource: SourceAPI,
	}

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

	out := &SubredditList{After: listing.Data.After, DataSource: SourceAPI, Subreddits: []Subreddit{}}

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
