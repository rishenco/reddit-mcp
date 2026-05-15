package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) BrowseSubreddit(
	ctx context.Context,
	subreddit,
	sort,
	timeFilter,
	after string,
	limit int,
) (*PostList, error) {
	if subreddit == "" {
		return nil, errors.New("subreddit is required")
	}

	if sort == "" {
		sort = "hot"
	}

	path, err := url.JoinPath("/r", normalizeSubreddit(subreddit), sort)
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, listingParams(after, limit, sort, timeFilter))
	if err != nil {
		return nil, fmt.Errorf("browse subreddit: %w", err)
	}

	list, err := decodePostListing(body)
	if err != nil {
		return nil, fmt.Errorf("browse subreddit: %w", err)
	}

	return list, nil
}

func (c *Client) Frontpage(
	ctx context.Context, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	if sort == "" {
		sort = "hot"
	}

	path, err := url.JoinPath("/", sort)
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, listingParams(after, limit, sort, timeFilter))
	if err != nil {
		return nil, fmt.Errorf("frontpage: %w", err)
	}

	list, err := decodePostListing(body)
	if err != nil {
		return nil, fmt.Errorf("frontpage: %w", err)
	}

	return list, nil
}

func (c *Client) SearchReddit(
	ctx context.Context, query, subreddit, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	if query == "" {
		return nil, errors.New("query is required")
	}

	path := "/search"

	if subreddit != "" {
		var err error

		path, err = url.JoinPath("/r", normalizeSubreddit(subreddit), "search")
		if err != nil {
			return nil, fmt.Errorf("build path: %w", err)
		}
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if subreddit != "" {
		params.Set("restrict_sr", "1")
	}

	if sort != "" {
		params.Set("sort", sort)
	}

	if timeFilter != "" {
		params.Set("t", timeFilter)
	}

	if after != "" {
		params.Set("after", after)
	}

	body, err := c.get(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("search reddit: %w", err)
	}

	list, err := decodePostListing(body)
	if err != nil {
		return nil, fmt.Errorf("search reddit: %w", err)
	}

	return list, nil
}

func (c *Client) GetPost(ctx context.Context, postID string) (*Post, error) {
	id := normalizePostID(postID)
	if id == "" {
		return nil, errors.New("post_id is required")
	}

	path, err := url.JoinPath("/by_id", "t3_"+id)
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get post: %w", err)
	}

	list, err := decodePostListing(body)
	if err != nil {
		return nil, fmt.Errorf("get post: %w", err)
	}

	if len(list.Posts) == 0 {
		return nil, fmt.Errorf("post %q not found", postID)
	}

	post := list.Posts[0]

	return &post, nil
}

func (c *Client) GetPostComments(
	ctx context.Context, postID, commentSort string, limit, depth int,
) (*Post, []Comment, error) {
	id := normalizePostID(postID)
	if id == "" {
		return nil, nil, errors.New("post_id is required")
	}

	params := url.Values{}
	if commentSort != "" {
		params.Set("sort", commentSort)
	}

	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if depth > 0 {
		params.Set("depth", strconv.Itoa(depth))
	}

	path, err := url.JoinPath("/comments", id)
	if err != nil {
		return nil, nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, params)
	if err != nil {
		return nil, nil, fmt.Errorf("get post comments: %w", err)
	}

	var pair []json.RawMessage
	if err := json.Unmarshal(body, &pair); err != nil {
		return nil, nil, fmt.Errorf("decode comments envelope: %w", err)
	}

	if len(pair) < 2 {
		return nil, nil, fmt.Errorf("unexpected comments response (got %d listings, want 2)", len(pair))
	}

	postList, err := decodePostListing(pair[0])
	if err != nil {
		return nil, nil, fmt.Errorf("get post comments: %w", err)
	}

	if len(postList.Posts) == 0 {
		return nil, nil, fmt.Errorf("post %q not found", postID)
	}

	comments, err := decodeCommentListing(pair[1])
	if err != nil {
		return nil, nil, fmt.Errorf("get post comments: %w", err)
	}

	post := postList.Posts[0]

	return &post, comments, nil
}

func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	name := normalizeUsername(username)
	if name == "" {
		return nil, errors.New("username is required")
	}

	path, err := url.JoinPath("/user", name, "about")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	var wrap struct {
		Kind string  `json:"kind"`
		Data rawUser `json:"data"`
	}

	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("decode user: %w", err)
	}

	user := wrap.Data.toUser()

	return &user, nil
}

func (c *Client) GetUserPosts(
	ctx context.Context, username, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	name := normalizeUsername(username)
	if name == "" {
		return nil, errors.New("username is required")
	}

	path, err := url.JoinPath("/user", name, "submitted")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	params := userListingParams(after, limit, sort, timeFilter)

	body, err := c.get(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("get user posts: %w", err)
	}

	list, err := decodePostListing(body)
	if err != nil {
		return nil, fmt.Errorf("get user posts: %w", err)
	}

	return list, nil
}

func (c *Client) GetUserComments(
	ctx context.Context, username, sort, timeFilter, after string, limit int,
) (*CommentList, error) {
	name := normalizeUsername(username)
	if name == "" {
		return nil, errors.New("username is required")
	}

	path, err := url.JoinPath("/user", name, "comments")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	params := userListingParams(after, limit, sort, timeFilter)

	body, err := c.get(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("get user comments: %w", err)
	}

	comments, err := decodeCommentListing(body)
	if err != nil {
		return nil, fmt.Errorf("get user comments: %w", err)
	}

	var cursor struct {
		Data struct {
			After string `json:"after"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &cursor); err != nil {
		return nil, fmt.Errorf("decode user comments cursor: %w", err)
	}

	return &CommentList{Comments: comments, After: cursor.Data.After}, nil
}

func (c *Client) GetSubredditInfo(ctx context.Context, name string) (*Subreddit, error) {
	sub := normalizeSubreddit(name)
	if sub == "" {
		return nil, errors.New("subreddit is required")
	}

	path, err := url.JoinPath("/r", sub, "about")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, nil)
	if err != nil {
		return nil, fmt.Errorf("get subreddit info: %w", err)
	}

	var wrap struct {
		Kind string       `json:"kind"`
		Data rawSubreddit `json:"data"`
	}

	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("decode subreddit: %w", err)
	}

	sr := wrap.Data.toSubreddit()

	return &sr, nil
}

func (c *Client) TrendingSubreddits(ctx context.Context, after string, limit int) (*SubredditList, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if after != "" {
		params.Set("after", after)
	}

	body, err := c.get(ctx, "/subreddits/popular", params)
	if err != nil {
		return nil, fmt.Errorf("trending subreddits: %w", err)
	}

	list, err := decodeSubredditListing(body)
	if err != nil {
		return nil, fmt.Errorf("trending subreddits: %w", err)
	}

	return list, nil
}

func listingParams(after string, limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if after != "" {
		params.Set("after", after)
	}

	if timeFilter != "" && (sort == "top" || sort == "controversial") {
		params.Set("t", timeFilter)
	}

	return params
}

func userListingParams(after string, limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if sort != "" {
		params.Set("sort", sort)
	}

	if timeFilter != "" {
		params.Set("t", timeFilter)
	}

	if after != "" {
		params.Set("after", after)
	}

	return params
}

func clampLimit(limit int) int {
	const (
		defaultLimit = 25
		maxLimit     = 100
	)

	if limit <= 0 {
		return defaultLimit
	}

	if limit > maxLimit {
		return maxLimit
	}

	return limit
}

func normalizeSubreddit(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "r/")

	return name
}

func normalizeUsername(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "u/")
	name = strings.TrimPrefix(name, "user/")

	return name
}

func normalizePostID(rawID string) string {
	return strings.TrimPrefix(strings.TrimSpace(rawID), "t3_")
}
