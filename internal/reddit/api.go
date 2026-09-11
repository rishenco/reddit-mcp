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
	// maxBatchIDs is what /by_id takes comfortably in one URL.
	maxBatchIDs = 50
	// maxSubreddits bounds a combined r/a+b+c listing.
	maxSubreddits = 20
)

var (
	postSorts      = []string{"hot", "new", "top", "rising", "controversial"}
	searchSorts    = []string{"relevance", "hot", "new", "top", "comments"}
	userSorts      = []string{"new", "top", "hot", "controversial"}
	commentSorts   = []string{"best", "top", "new", "controversial", "old", "qa"}
	timeFilters    = []string{"hour", "day", "week", "month", "year", "all"}
	subredditFinds = []string{"search", "popular", "new"}
)

// --- listings ------------------------------------------------------------

// BrowseSubreddit lists posts from one or more subreddits. Several names
// combine into a single request, which is both cheaper against the rate limit
// and the only way to get a merged ranking across communities. "all" and
// "popular" are the site-wide feeds.
func (c *Client) BrowseSubreddit(
	ctx context.Context, subreddits, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	name, err := normalizeSubreddits(subreddits)
	if err != nil {
		return nil, err
	}

	sort = defaultTo(sort, "hot")
	if err := validate("sort", sort, postSorts); err != nil {
		return nil, err
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	body, err := c.get(ctx, "/r/"+name+"/"+sort, listingParams(after, limit, sort, timeFilter))
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

func (c *Client) SearchReddit(
	ctx context.Context, query, subreddits, sort, timeFilter, after string, limit int,
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

	path, rssPath := "/search", "/search.rss"

	if strings.TrimSpace(subreddits) != "" {
		name, err := normalizeSubreddits(subreddits)
		if err != nil {
			return nil, err
		}

		path, rssPath = "/r/"+name+"/search", "/r/"+name+"/search.rss"
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "sort", sort)
	setIfNotEmpty(params, "t", timeFilter)

	if path != "/search" {
		params.Set("restrict_sr", "1")
	}

	rssQuery := cloneValues(params)

	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, path, params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("search reddit: %w", err)
		}

		return c.postsFromRSS(ctx, rssPath, rssQuery)
	}

	list, err := decodePostListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("search reddit: %w", err)
	}

	return list, nil
}

// BrowseComments reads a comment stream: every recent comment in a subreddit,
// or every recent comment by a user. The subreddit stream is the only way to
// see what a community is saying without picking threads first.
func (c *Client) BrowseComments(
	ctx context.Context, subreddit, username, sort, timeFilter, after string, limit int,
) (*CommentList, error) {
	var path, rssPath string

	switch {
	case strings.TrimSpace(subreddit) != "" && strings.TrimSpace(username) != "":
		return nil, errors.New("pass either subreddit or username, not both")
	case strings.TrimSpace(subreddit) != "":
		name, err := normalizeSubreddits(subreddit)
		if err != nil {
			return nil, err
		}

		path, rssPath = "/r/"+name+"/comments", "/r/"+name+"/comments/.rss"
	case strings.TrimSpace(username) != "":
		name, err := normalizeUsername(username)
		if err != nil {
			return nil, err
		}

		path, rssPath = "/user/"+name+"/comments", "/user/"+name+"/comments/.rss"
	default:
		return nil, errors.New("subreddit or username is required")
	}

	if sort != "" {
		if err := validate("sort", sort, userSorts); err != nil {
			return nil, err
		}
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	body, err := c.get(ctx, path, userListingParams(after, limit, sort, timeFilter))
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("browse comments: %w", err)
		}

		raw, rerr := c.getRSS(ctx, rssPath, rssParams(limit, sort, timeFilter))
		if rerr != nil {
			return nil, fmt.Errorf("browse comments (rss): %w", rerr)
		}

		return parseRSSComments(raw, c.listingText)
	}

	list, err := decodeCommentListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("browse comments: %w", err)
	}

	return list, nil
}

// --- posts ---------------------------------------------------------------

// GetPosts fetches posts by id. Reddit's /by_id takes a comma-separated list,
// so "look at these ten links" costs one request rather than ten.
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

	body, err := c.get(ctx, "/by_id/"+strings.Join(fullnames, ","), nil)
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

// FindPostsByURL answers "did anyone post this link, and what did they say".
// Reddit indexes submissions by their target URL, which no search query reaches
// reliably.
func (c *Client) FindPostsByURL(ctx context.Context, target string, limit int) (*PostList, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("url is required")
	}

	if _, err := url.ParseRequestURI(target); err != nil {
		return nil, fmt.Errorf("url %q is not a valid absolute URL: %w", target, err)
	}

	params := url.Values{}
	params.Set("url", target)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	body, err := c.get(ctx, "/api/info", params)
	if err != nil {
		return nil, fmt.Errorf("find posts by url: %w", err)
	}

	list, err := decodePostListing(body, c.listingText)
	if err != nil {
		return nil, fmt.Errorf("find posts by url: %w", err)
	}

	return list, nil
}

// FindDuplicates lists the other threads discussing the same link, which is
// where a story's actual discussion often lives once a repost outgrows the
// original.
func (c *Client) FindDuplicates(ctx context.Context, postID, after string, limit int) (*PostList, error) {
	id, err := normalizePostID(postID)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, "/duplicates/"+id, params)
	if err != nil {
		return nil, fmt.Errorf("find duplicates: %w", err)
	}

	// Two listings: the original post, then the duplicates.
	var pair []json.RawMessage
	if err := json.Unmarshal(body, &pair); err != nil {
		return nil, fmt.Errorf("decode duplicates: %w", err)
	}

	const wantListings = 2
	if len(pair) < wantListings {
		return nil, fmt.Errorf("unexpected duplicates response (got %d listings, want %d)",
			len(pair), wantListings)
	}

	list, err := decodePostListing(pair[1], c.listingText)
	if err != nil {
		return nil, fmt.Errorf("find duplicates: %w", err)
	}

	return list, nil
}

// GetPostComments returns a post with its comment tree. With commentID set,
// Reddit returns that comment's subtree instead, which is how the branches in
// CommentList.MoreParentIDs get expanded.
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

	body, err := c.get(ctx, "/comments/"+id, params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, nil, fmt.Errorf("get post comments: %w", err)
		}

		return c.commentsFromRSS(ctx, id, clampLimit(limit))
	}

	post, comments, err := decodeCommentsEnvelope(body, c.commentText)
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

// --- communities ---------------------------------------------------------

// FindSubreddits discovers communities: by query, or by browsing the popular
// and newly created lists when no query is given.
func (c *Client) FindSubreddits(ctx context.Context, query, sort, after string, limit int) (*SubredditList, error) {
	sort = defaultTo(sort, "search")
	if err := validate("sort", sort, subredditFinds); err != nil {
		return nil, err
	}

	if sort == "search" && strings.TrimSpace(query) == "" {
		return nil, errors.New(`query is required when sort is "search"`)
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "after", after)

	path := "/subreddits/" + sort
	rssPath := "/subreddits/" + sort + ".rss"
	rssQuery := url.Values{"limit": []string{strconv.Itoa(clampLimit(limit))}}

	if sort == "search" {
		params.Set("q", query)
		rssQuery.Set("q", query)
	}

	body, err := c.get(ctx, path, params)
	if err != nil {
		if !IsBlocked(err) {
			return nil, fmt.Errorf("find subreddits: %w", err)
		}

		return c.subredditsFromRSS(ctx, rssPath, rssQuery)
	}

	list, err := decodeSubredditListing(body)
	if err != nil {
		return nil, fmt.Errorf("find subreddits: %w", err)
	}

	return list, nil
}

func (c *Client) GetSubredditInfo(ctx context.Context, name string, includeRules bool) (*Subreddit, error) {
	sub, err := normalizeSubreddit(name)
	if err != nil {
		return nil, err
	}

	body, err := c.get(ctx, "/r/"+sub+"/about", nil)
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

	info := wrap.Data.toSubreddit()

	if includeRules {
		rulesBody, rerr := c.get(ctx, "/r/"+sub+"/about/rules", nil)
		if rerr != nil {
			return nil, fmt.Errorf("get subreddit rules: %w", rerr)
		}

		rules, rerr := decodeRules(rulesBody)
		if rerr != nil {
			return nil, rerr
		}

		info.Rules = rules
	}

	return &info, nil
}

// GetWikiPage reads a subreddit's wiki. Communities keep their FAQs, guides and
// recommendation lists there, and none of it appears in any listing.
func (c *Client) GetWikiPage(ctx context.Context, subreddit, page string) (*WikiPage, error) {
	sub, err := normalizeSubreddit(subreddit)
	if err != nil {
		return nil, err
	}

	name, err := normalizeWikiPage(page)
	if err != nil {
		return nil, err
	}

	body, err := c.get(ctx, "/r/"+sub+"/wiki/"+name, nil)
	if err != nil {
		return nil, fmt.Errorf("get wiki page: %w", err)
	}

	return decodeWikiPage(body, sub, name)
}

// --- users ---------------------------------------------------------------

func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	name, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}

	body, err := c.get(ctx, "/user/"+name+"/about", nil)
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

func (c *Client) SearchUsers(ctx context.Context, query, after string, limit int) (*UserList, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(clampLimit(limit)))
	setIfNotEmpty(params, "after", after)

	body, err := c.get(ctx, "/users/search", params)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}

	list, err := decodeUserListing(body)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}

	return list, nil
}

func (c *Client) GetUserPosts(
	ctx context.Context, username, sort, timeFilter, after string, limit int,
) (*PostList, error) {
	name, err := normalizeUsername(username)
	if err != nil {
		return nil, err
	}

	if sort != "" {
		if err := validate("sort", sort, userSorts); err != nil {
			return nil, err
		}
	}

	if err := validateTimeFilter(timeFilter); err != nil {
		return nil, err
	}

	body, err := c.get(ctx, "/user/"+name+"/submitted", userListingParams(after, limit, sort, timeFilter))
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
// simply unavailable without credentials.
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

	comments, err := parseRSSComments(raw, c.commentText)
	if err != nil {
		return nil, nil, err
	}

	return &posts.Posts[0], comments, nil
}

// postsFromCommentFeeds is the batch fallback: /by_id has no feed equivalent,
// so each post costs one request.
func (c *Client) postsFromCommentFeeds(ctx context.Context, fullnames []string) (*PostList, error) {
	out := &PostList{source: rssSource()}

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

// --- parameters ----------------------------------------------------------

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

// rssParams mirrors the listing parameters the feeds accept. They have no
// cursor: Atom feeds are not paginated.
func rssParams(limit int, sort, timeFilter string) url.Values {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(clampLimit(limit)))

	if timeFilter != "" && (sort == "top" || sort == "controversial" || sort == "") {
		setIfNotEmpty(params, "t", timeFilter)
	}

	return params
}

func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for key, values := range in {
		out[key] = append([]string(nil), values...)
	}

	return out
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

// --- normalization -------------------------------------------------------

var (
	// Names go straight into the request path, so they are validated against
	// Reddit's own character sets rather than escaped and hoped for.
	subredditRE = regexp.MustCompile(`^[A-Za-z0-9_]{2,25}$`)
	usernameRE  = regexp.MustCompile(`^[A-Za-z0-9_-]{2,20}$`)
	wikiPageRE  = regexp.MustCompile(`^[A-Za-z0-9_/-]{1,100}$`)
	// commentsPathRE finds the post id inside any reddit.com permalink,
	// including a comment permalink, where /comments/ still names the post.
	commentsPathRE = regexp.MustCompile(`(?i)/comments/([a-z0-9]+)`)
	base36RE       = regexp.MustCompile(`(?i)^[a-z0-9]{2,16}$`)
	subSplitRE     = regexp.MustCompile(`[+,\s]+`)
)

func normalizeSubreddit(name string) (string, error) {
	clean := strings.Trim(strings.TrimSpace(name), "/")
	clean = strings.TrimPrefix(clean, "r/")
	clean = strings.Trim(clean, "/")

	if clean == "" {
		return "", errors.New("subreddit is required")
	}

	if !subredditRE.MatchString(clean) {
		return "", fmt.Errorf("%q is not a subreddit name (letters, digits and underscore, 2-25 chars)", name)
	}

	return clean, nil
}

// normalizeSubreddits accepts one name or several, separated by +, commas or
// spaces, and returns Reddit's a+b+c form.
func normalizeSubreddits(names string) (string, error) {
	fields := subSplitRE.Split(strings.TrimSpace(names), -1)

	out := make([]string, 0, len(fields))

	for _, field := range fields {
		if field == "" {
			continue
		}

		name, err := normalizeSubreddit(field)
		if err != nil {
			return "", err
		}

		out = append(out, name)
	}

	if len(out) == 0 {
		return "", errors.New("subreddit is required")
	}

	if len(out) > maxSubreddits {
		return "", fmt.Errorf("too many subreddits: %d (max %d)", len(out), maxSubreddits)
	}

	return strings.Join(out, "+"), nil
}

func normalizeUsername(name string) (string, error) {
	clean := strings.Trim(strings.TrimSpace(name), "/")
	clean = strings.TrimPrefix(clean, "user/")
	clean = strings.TrimPrefix(clean, "u/")
	clean = strings.Trim(clean, "/")

	if clean == "" {
		return "", errors.New("username is required")
	}

	if !usernameRE.MatchString(clean) {
		return "", fmt.Errorf("%q is not a username (letters, digits, underscore and dash, 2-20 chars)", name)
	}

	return clean, nil
}

func normalizeWikiPage(page string) (string, error) {
	clean := strings.Trim(strings.TrimSpace(page), "/")
	if clean == "" {
		return "index", nil
	}

	if !wikiPageRE.MatchString(clean) {
		return "", fmt.Errorf("%q is not a wiki page path", page)
	}

	return clean, nil
}

// normalizePostID accepts everything a model is likely to be holding: a bare
// id, a t3_ fullname, a permalink, a comment permalink or a redd.it link.
// Models work from URLs far more often than from base36 ids.
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

	return value, nil
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

	return value, nil
}
