package reddit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultLimit = 25
	maxLimit     = 100
	maxDepth     = 10
	// maxBatchIDs matches what /by_id accepts comfortably in a single URL.
	maxBatchIDs = 50
)

var (
	postSorts    = []string{"hot", "new", "top", "rising", "controversial"}
	searchSorts  = []string{"relevance", "hot", "new", "top", "comments"}
	userSorts    = []string{"new", "top", "hot", "controversial"}
	commentSorts = []string{"best", "top", "new", "controversial", "old", "qa"}
	timeFilters  = []string{"hour", "day", "week", "month", "year", "all"}
)

func (c *Client) BrowseSubreddit(
	ctx context.Context,
	subreddit,
	sort,
	timeFilter,
	after string,
	limit int,
) (*PostList, error) {
	name := normalizeSubreddit(subreddit)
	if name == "" {
		return nil, errors.New("subreddit is required")
	}

	sort = defaultTo(sort, "hot")
	if err := validate("sort", sort, postSorts); err != nil {
		return nil, err
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	path, err := url.JoinPath("/r", name, sort)
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, listingParams(after, limit, sort, timeFilter))
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("browse subreddit: %w", err)
		}

		return c.postsFromRSS(ctx, "/r/"+name+"/"+sort+"/.rss", rssParams(limit, sort, timeFilter))
	}

	list, err := decodePostListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("browse subreddit: %w", err)
	}

	return list, nil
}

func (c *Client) Frontpage(
	ctx context.Context, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	sort = defaultTo(sort, "hot")
	if err := validate("sort", sort, postSorts); err != nil {
		return nil, err
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	path, err := url.JoinPath("/", sort)
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, listingParams(after, limit, sort, timeFilter))
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("frontpage: %w", err)
		}

		return c.postsFromRSS(ctx, "/"+sort+"/.rss", rssParams(limit, sort, timeFilter))
	}

	list, err := decodePostListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("frontpage: %w", err)
	}

	return list, nil
}

func (c *Client) SearchReddit(
	ctx context.Context, query, subreddit, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}

	if sort != "" {
		if err := validate("sort", sort, searchSorts); err != nil {
			return nil, err
		}
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	name := normalizeSubreddit(subreddit)

	path := "/search"
	rssPath := "/search.rss"

	if name != "" {
		var err error

		path, err = url.JoinPath("/r", name, "search")
		if err != nil {
			return nil, fmt.Errorf("build path: %w", err)
		}

		rssPath = "/r/" + name + "/search.rss"
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if name != "" {
		params.Set("restrict_sr", "1")
	}

	setIfNotEmpty(params, "sort", sort)
	setIfNotEmpty(params, "t", timeFilter)
	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, path, params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("search reddit: %w", err)
		}

		rssQuery := url.Values{}
		rssQuery.Set("q", query)
		rssQuery.Set("limit", strconv.Itoa(clampLimit(limit)))
		setIfNotEmpty(rssQuery, "sort", sort)
		setIfNotEmpty(rssQuery, "t", timeFilter)

		if name != "" {
			rssQuery.Set("restrict_sr", "1")
		}

		return c.postsFromRSS(ctx, rssPath, rssQuery)
	}

	list, err := decodePostListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("search reddit: %w", err)
	}

	return list, nil
}

func (c *Client) GetPost(ctx context.Context, postID string) (*Post, error) {
	posts, err := c.GetPosts(ctx, []string{postID})
	if err != nil {
		return nil, err
	}

	if len(posts.Posts) == 0 {
		return nil, fmt.Errorf("post %q not found", postID)
	}

	return &posts.Posts[0], nil
}

// GetPosts fetches several posts in one request. Reddit's /by_id takes a
// comma-separated list of fullnames, which turns "look at these ten links" from
// ten requests against a 100/minute budget into one.
func (c *Client) GetPosts(ctx context.Context, postIDs []string) (*PostList, error) {
	if len(postIDs) == 0 {
		return nil, errors.New("post_ids is required")
	}

	if len(postIDs) > maxBatchIDs {
		return nil, fmt.Errorf("too many post_ids: %d (max %d)", len(postIDs), maxBatchIDs)
	}

	fullnames := make([]string, 0, len(postIDs))

	for _, raw := range postIDs {
		id, err := normalizePostID(raw)
		if err != nil {
			return nil, err
		}

		fullnames = append(fullnames, "t3_"+id)
	}

	path, err := url.JoinPath("/by_id", strings.Join(fullnames, ","))
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, nil)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("get posts: %w", err)
		}

		return c.postsFromCommentFeeds(ctx, fullnames)
	}

	list, err := decodePostListing(body, 0)
	if err != nil {
		return nil, fmt.Errorf("get posts: %w", err)
	}

	return list, nil
}

// GetPostComments returns a post with its comment tree. When commentID is set,
// Reddit returns that comment's subtree instead of the whole thread, which is
// how a caller expands the branches reported in CommentList.MoreParentIDs.
func (c *Client) GetPostComments(
	ctx context.Context, postID, commentID, commentSort string, limit, depth int,
) (*Post, *CommentList, error) {
	id, err := normalizePostID(postID)
	if err != nil {
		return nil, nil, err
	}

	if commentSort != "" {
		if verr := validate("comment_sort", commentSort, commentSorts); verr != nil {
			return nil, nil, verr
		}
	}

	params := url.Values{}
	setIfNotEmpty(params, "sort", commentSort)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if depth := clampDepth(depth); depth > 0 {
		params.Set("depth", strconv.Itoa(depth))
	}

	if commentID != "" {
		cid, cerr := normalizeCommentID(commentID)
		if cerr != nil {
			return nil, nil, cerr
		}

		params.Set("comment", cid)
		params.Set("context", "0")
	}

	path, err := url.JoinPath("/comments", id)
	if err != nil {
		return nil, nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, nil, fmt.Errorf("get post comments: %w", err)
		}

		return c.commentsFromRSS(ctx, id, clampLimit(limit))
	}

	post, comments, err := decodeCommentsEnvelope(body, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("get post comments: %w", err)
	}

	return post, comments, nil
}

// decodeCommentsEnvelope unpacks the two-listing array Reddit returns for a
// comments page: the post first, then its comments.
func decodeCommentsEnvelope(body json.RawMessage, maxText int) (*Post, *CommentList, error) {
	var pair []json.RawMessage
	if err := json.Unmarshal(body, &pair); err != nil {
		return nil, nil, fmt.Errorf("decode comments envelope: %w", err)
	}

	const wantListings = 2
	if len(pair) < wantListings {
		return nil, nil, fmt.Errorf("unexpected comments response (got %d listings, want %d)",
			len(pair), wantListings)
	}

	postList, err := decodePostListing(pair[0], 0)
	if err != nil {
		return nil, nil, err
	}

	if len(postList.Posts) == 0 {
		return nil, nil, errors.New("post not found")
	}

	comments, err := decodeCommentListing(pair[1], maxText)
	if err != nil {
		return nil, nil, err
	}

	return &postList.Posts[0], comments, nil
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

	if sort != "" {
		if err := validate("sort", sort, userSorts); err != nil {
			return nil, err
		}
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	path, err := url.JoinPath("/user", name, "submitted")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, userListingParams(after, limit, sort, timeFilter))
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("get user posts: %w", err)
		}

		return c.postsFromRSS(ctx, "/user/"+name+"/submitted/.rss", rssParams(limit, sort, timeFilter))
	}

	list, err := decodePostListing(body, c.listingText)
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

	if sort != "" {
		if err := validate("sort", sort, userSorts); err != nil {
			return nil, err
		}
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	path, err := url.JoinPath("/user", name, "comments")
	if err != nil {
		return nil, fmt.Errorf("build path: %w", err)
	}

	body, err := c.get(ctx, path, userListingParams(after, limit, sort, timeFilter))
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("get user comments: %w", err)
		}

		raw, rerr := c.getRSS(ctx, "/user/"+name+"/comments/.rss", rssParams(limit, sort, timeFilter))
		if rerr != nil {
			return nil, fmt.Errorf("get user comments (rss): %w", rerr)
		}

		return parseRSSComments(raw, c.listingText)
	}

	list, err := decodeCommentListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("get user comments: %w", err)
	}

	return list, nil
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
		if !IsBlocked(err) {
			return nil, fmt.Errorf("get subreddit info: %w", err)
		}

		return c.subredditFromRSS(ctx, sub)
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
	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, "/subreddits/popular", params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("trending subreddits: %w", err)
		}

		return c.subredditsFromRSS(ctx, "/subreddits/popular.rss", rssParams(limit, "", ""))
	}

	list, err := decodeSubredditListing(body)
	if err != nil {
		return nil, fmt.Errorf("trending subreddits: %w", err)
	}

	return list, nil
}

// SearchSubreddits finds communities by name or topic, which is the step before
// browsing: "where is this discussed" cannot be answered by a trending list.
func (c *Client) SearchSubreddits(ctx context.Context, query, after string, limit int) (*SubredditList, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, "/subreddits/search", params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("search subreddits: %w", err)
		}

		rssQuery := url.Values{}
		rssQuery.Set("q", query)
		rssQuery.Set("limit", strconv.Itoa(clampLimit(limit)))

		return c.subredditsFromRSS(ctx, "/subreddits/search.rss", rssQuery)
	}

	list, err := decodeSubredditListing(body)
	if err != nil {
		return nil, fmt.Errorf("search subreddits: %w", err)
	}

	return list, nil
}

// --- RSS fallbacks -------------------------------------------------------

func (c *Client) postsFromRSS(ctx context.Context, path string, params url.Values) (*PostList, error) {
	raw, err := c.getRSS(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("rss fallback %s: %w", path, err)
	}

	return parseRSSPosts(raw, c.listingText)
}

func (c *Client) subredditsFromRSS(ctx context.Context, path string, params url.Values) (*SubredditList, error) {
	raw, err := c.getRSS(ctx, path, params)
	if err != nil {
		return nil, fmt.Errorf("rss fallback %s: %w", path, err)
	}

	return parseRSSSubreddits(raw)
}

// subredditFromRSS reconstructs what it can of a subreddit's metadata from its
// listing feed. There is no about.rss, so subscriber and activity counts are
// simply not available without credentials.
func (c *Client) subredditFromRSS(ctx context.Context, name string) (*Subreddit, error) {
	raw, err := c.getRSS(ctx, "/r/"+name+"/.rss", url.Values{"limit": []string{"1"}})
	if err != nil {
		return nil, fmt.Errorf("rss fallback for r/%s: %w", name, err)
	}

	feed, err := parseFeed(raw)
	if err != nil {
		return nil, err
	}

	return &Subreddit{
		Name:  name,
		Title: cleanText(feed.Title),
		URL:   anonHost + "/r/" + name + "/",
	}, nil
}

// commentsFromRSS reads a thread from /comments/{id}/.rss, whose first entry is
// the post itself and the rest are its comments. The feed is flat, so replies
// carry no depth or parent.
func (c *Client) commentsFromRSS(ctx context.Context, postID string, limit int) (*Post, *CommentList, error) {
	raw, err := c.getRSS(ctx, "/comments/"+postID+"/.rss", url.Values{
		"limit": []string{strconv.Itoa(limit)},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("rss fallback for post %s: %w", postID, err)
	}

	posts, err := parseRSSPosts(raw, 0)
	if err != nil {
		return nil, nil, err
	}

	if len(posts.Posts) == 0 {
		return nil, nil, fmt.Errorf("post %q not found", postID)
	}

	comments, err := parseRSSComments(raw, 0)
	if err != nil {
		return nil, nil, err
	}

	return &posts.Posts[0], comments, nil
}

// postsFromCommentFeeds is the batch fallback: /by_id has no RSS equivalent, so
// each post costs one feed request.
func (c *Client) postsFromCommentFeeds(ctx context.Context, fullnames []string) (*PostList, error) {
	out := &PostList{DataSource: SourceRSS, Note: rssNote, Posts: []Post{}}

	for _, fullname := range fullnames {
		id := strings.TrimPrefix(fullname, "t3_")

		raw, err := c.getRSS(ctx, "/comments/"+id+"/.rss", url.Values{"limit": []string{"1"}})
		if err != nil {
			return nil, fmt.Errorf("rss fallback for post %s: %w", id, err)
		}

		posts, err := parseRSSPosts(raw, 0)
		if err != nil {
			return nil, err
		}

		out.Posts = append(out.Posts, posts.Posts...)
	}

	return out, nil
}

// --- parameters and normalization ---------------------------------------

func listingParams(after string, limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "after", after)

	if timeFilter != "" && (sort == "top" || sort == "controversial") {
		params.Set("t", timeFilter)
	}

	return params
}

func userListingParams(after string, limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "sort", sort)
	setIfNotEmpty(params, "t", timeFilter)
	setIfNotEmpty(params, "after", after)

	return params
}

// rssParams mirrors listingParams for the feeds, which accept limit, sort and t
// but have no cursor: Atom feeds are not paginated.
func rssParams(limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if timeFilter != "" && (sort == "top" || sort == "controversial" || sort == "") {
		setIfNotEmpty(params, "t", timeFilter)
	}

	return params
}

func setIfNotEmpty(params url.Values, key, value string) {
	if value != "" {
		params.Set(key, value)
	}
}

func defaultTo(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func validate(field, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}

	return fmt.Errorf("invalid %s %q: want one of %s", field, value, strings.Join(allowed, "|"))
}

func validateTimeFilter(value string) error {
	if value == "" {
		return nil
	}

	return validate("time_filter", value, timeFilters)
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}

	return min(limit, maxLimit)
}

func clampDepth(depth int) int {
	if depth <= 0 {
		return 0
	}

	return min(depth, maxDepth)
}

func normalizeSubreddit(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "r/")

	return strings.Trim(name, "/")
}

func normalizeUsername(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimPrefix(name, "u/")
	name = strings.TrimPrefix(name, "user/")

	return strings.Trim(name, "/")
}

var (
	// commentsPathRE finds the post id inside any reddit.com permalink, including
	// a comment permalink, where the post id is still the /comments/ segment.
	commentsPathRE = regexp.MustCompile(`(?i)/comments/([a-z0-9]+)`)
	base36RE       = regexp.MustCompile(`(?i)^[a-z0-9]{2,16}$`)
)

// normalizePostID accepts everything a model is likely to be holding: a bare
// id, a t3_ fullname, a full permalink, a comment permalink or a redd.it short
// link. Models work from URLs far more often than from base36 ids.
func normalizePostID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("post_id is required")
	}

	if m := commentsPathRE.FindStringSubmatch(value); m != nil {
		return strings.ToLower(m[1]), nil
	}

	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && strings.EqualFold(parsed.Host, "redd.it") {
			value = strings.Trim(parsed.Path, "/")
		}
	}

	// Lowercase before trimming the prefix: a pasted "T3_..." is still a fullname.
	value = strings.TrimPrefix(strings.ToLower(value), "t3_")
	value = strings.Trim(value, "/")

	if !base36RE.MatchString(value) {
		return "", fmt.Errorf(
			"cannot read a post id from %q: pass a base36 id (1wa14q6), a t3_ fullname, "+
				"a reddit.com permalink or a redd.it link", raw)
	}

	return strings.ToLower(value), nil
}

func normalizeCommentID(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.TrimPrefix(value, "t1_")
	value = strings.Trim(value, "/")

	// A comment permalink ends with the comment id after the post slug.
	if idx := strings.LastIndex(value, "/"); idx >= 0 && strings.Contains(value, "/comments/") {
		value = value[idx+1:]
	}

	if !base36RE.MatchString(value) {
		return "", fmt.Errorf("cannot read a comment id from %q: pass a base36 id or a t1_ fullname", raw)
	}

	return strings.ToLower(value), nil
}
