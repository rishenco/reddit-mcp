package reddit

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Post struct {
	ID            string  `json:"id"`
	Subreddit     string  `json:"subreddit"`
	Title         string  `json:"title"`
	Author        string  `json:"author"`
	URL           string  `json:"url"`
	Permalink     string  `json:"permalink"`
	Score         int     `json:"score"`
	UpvoteRatio   float64 `json:"upvote_ratio"`
	NumComments   int     `json:"num_comments"`
	CreatedUTC    int64   `json:"created_utc"`
	IsSelf        bool    `json:"is_self"`
	Selftext      string  `json:"selftext,omitempty"`
	Over18        bool    `json:"over_18"`
	Stickied      bool    `json:"stickied"`
	Locked        bool    `json:"locked"`
	LinkFlairText string  `json:"link_flair_text,omitempty"`
}

// Comment is flattened: nested replies are emitted as separate entries
// with ParentID and Depth set, to keep the output schema non-recursive.
type Comment struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id,omitempty"`
	Depth       int    `json:"depth"`
	Author      string `json:"author"`
	Body        string `json:"body"`
	Score       int    `json:"score"`
	CreatedUTC  int64  `json:"created_utc"`
	Permalink   string `json:"permalink"`
	IsSubmitter bool   `json:"is_submitter"`
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
	Title         string `json:"title"`
	Description   string `json:"public_description"`
	Subscribers   int    `json:"subscribers"`
	ActiveUsers   int    `json:"active_user_count"`
	CreatedUTC    int64  `json:"created_utc"`
	Over18        bool   `json:"over_18"`
	Lang          string `json:"lang,omitempty"`
	URL           string `json:"url"`
	SubredditType string `json:"subreddit_type,omitempty"`
}

type PostList struct {
	Posts []Post `json:"posts"`
	After string `json:"after,omitempty"`
}

type CommentList struct {
	Comments []Comment `json:"comments"`
	After    string    `json:"after,omitempty"`
}

type SubredditList struct {
	Subreddits []Subreddit `json:"subreddits"`
	After      string      `json:"after,omitempty"`
}

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

func (r rawPost) toPost() Post {
	return Post{
		ID:            r.ID,
		Subreddit:     r.Subreddit,
		Title:         r.Title,
		Author:        r.Author,
		URL:           r.URL,
		Permalink:     absolutePermalink(r.Permalink),
		Score:         r.Score,
		UpvoteRatio:   r.UpvoteRatio,
		NumComments:   r.NumComments,
		CreatedUTC:    int64(r.CreatedUTC),
		IsSelf:        r.IsSelf,
		Selftext:      r.Selftext,
		Over18:        r.Over18,
		Stickied:      r.Stickied,
		Locked:        r.Locked,
		LinkFlairText: r.LinkFlairText,
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

func (r rawComment) flatten(parentID string, depth int, out *[]Comment) {
	*out = append(*out, Comment{
		ID:          r.ID,
		ParentID:    parentID,
		Depth:       depth,
		Author:      r.Author,
		Body:        r.Body,
		Score:       r.Score,
		CreatedUTC:  int64(r.CreatedUTC),
		Permalink:   absolutePermalink(r.Permalink),
		IsSubmitter: r.IsSubmitter,
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
		if ch.Kind != "t1" {
			continue
		}

		var child rawComment
		if err := json.Unmarshal(ch.Data, &child); err != nil {
			continue
		}

		child.flatten(r.ID, depth+1, out)
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
		Subscribers:   r.Subscribers,
		ActiveUsers:   r.ActiveUserCount,
		CreatedUTC:    int64(r.CreatedUTC),
		Over18:        r.Over18,
		Lang:          r.Lang,
		URL:           r.URL,
		SubredditType: r.SubredditType,
	}
}

func decodePostListing(body json.RawMessage) (*PostList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode post listing: %w", err)
	}

	out := &PostList{After: listing.Data.After}

	for _, ch := range listing.Data.Children {
		if ch.Kind != "t3" {
			continue
		}

		var rp rawPost
		if err := json.Unmarshal(ch.Data, &rp); err != nil {
			return nil, fmt.Errorf("decode post: %w", err)
		}

		out.Posts = append(out.Posts, rp.toPost())
	}

	return out, nil
}

func decodeCommentListing(body json.RawMessage) ([]Comment, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode comment listing: %w", err)
	}

	out := make([]Comment, 0, len(listing.Data.Children))

	for _, ch := range listing.Data.Children {
		if ch.Kind != "t1" {
			continue
		}

		var rc rawComment
		if err := json.Unmarshal(ch.Data, &rc); err != nil {
			return nil, fmt.Errorf("decode comment: %w", err)
		}

		rc.flatten("", 0, &out)
	}

	return out, nil
}

func decodeSubredditListing(body json.RawMessage) (*SubredditList, error) {
	var listing rawListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode subreddit listing: %w", err)
	}

	out := &SubredditList{After: listing.Data.After}

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

	return "https://www.reddit.com" + permalink
}
