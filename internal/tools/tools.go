package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rishenco/reddit-mcp/internal/reddit"
)

var (
	errNoTarget    = errors.New("pass either url or post_id")
	errBothTargets = errors.New("pass either url or post_id, not both")
)

// Instructions is sent once at initialize. It carries what the per-call
// schemas cannot: which tool to reach for, and that results may quietly be the
// degraded logged-out ones.
const Instructions = `Read-only access to Reddit.

Finding things:
- A topic across Reddit: search_reddit. Reddit's search operators work in the query
  (author:name, flair:"x", self:yes, site:domain.com).
- Where a topic lives: find_subreddits, then browse_subreddit. One call browses several
  communities at once: subreddit "golang+rust+cpp". "all" and "popular" are the site feeds.
- Whether a link was posted, and what was said about it: find_discussions.
- What a community is saying right now, without picking threads: browse_comments.
- A community's rules, FAQ or recommendation lists: get_subreddit_info(include_rules) and
  get_wiki_page — wiki content appears in no listing.

Reading results:
- A header marked (rss) is the logged-out fallback, used when Reddit refuses the Data API
  without credentials. It has no scores, comment counts, ratios or NSFW flags, and no
  pagination; "metrics unknown" there means unknown, not zero. Say so if it matters.
- Listings shorten post self-text; get_posts and get_post_comments return it whole.
- A thread reporting "more replies not loaded" is incomplete — expand a branch with
  get_post_comments(comment_id=...).
- Ids are base36 and links are absolute, so results can be cited directly. Anywhere a post
  is named, a permalink or redd.it link works too.

Reddit rate-limits hard, and every call costs the same whether it returns 1 item or 100.
Keep limit at its default unless asked for more, batch ids into get_posts, and combine
subreddits into one browse rather than looping.`

type browseInput struct {
	Subreddit  string `json:"subreddit"             jsonschema:"One subreddit, or several joined with + (golang+rust). Also 'all', 'popular'."`
	Sort       string `json:"sort,omitempty"        jsonschema:"hot|new|top|rising|controversial. Default: hot."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"For top/controversial only: hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max posts (1-100, default 25). Raise only if asked."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor from a previous response."`
}

type searchInput struct {
	Query      string `json:"query"                 jsonschema:"Search query; Reddit operators like author: flair: site: work here."`
	Subreddit  string `json:"subreddit,omitempty"   jsonschema:"Scope to this subreddit, or several joined with +."`
	Sort       string `json:"sort,omitempty"        jsonschema:"relevance|hot|new|top|comments. Default: relevance."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max results (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type browseCommentsInput struct {
	Subreddit  string `json:"subreddit,omitempty"   jsonschema:"Read every recent comment in this subreddit."`
	Username   string `json:"username,omitempty"    jsonschema:"Read this user's recent comments instead. Pass one of subreddit or username."`
	Sort       string `json:"sort,omitempty"        jsonschema:"new|top|hot|controversial. Default: new."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max comments (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type postIDsInput struct {
	PostIDs []string `json:"post_ids" jsonschema:"Up to 50 post ids, permalinks or redd.it links; fetched in one request."`
}

type discussionsInput struct {
	URL    string `json:"url,omitempty"     jsonschema:"Find posts linking to this exact URL. Pass either url or post_id."`
	PostID string `json:"post_id,omitempty" jsonschema:"Find other threads about the same link as this post."`
	Limit  int    `json:"limit,omitempty"   jsonschema:"Max results (1-100, default 25)."`
}

type postCommentsInput struct {
	PostID      string `json:"post_id"                jsonschema:"Post id, t3_ fullname, permalink or redd.it link."`
	CommentID   string `json:"comment_id,omitempty"   jsonschema:"Load this comment's subtree only. Use an id from 'more replies not loaded'."`
	CommentSort string `json:"comment_sort,omitempty" jsonschema:"best|top|new|controversial|old|qa. Default: best."`
	Limit       int    `json:"limit,omitempty"        jsonschema:"Max comments for the whole thread, replies included (1-100, default 25)."`
	Depth       int    `json:"depth,omitempty"        jsonschema:"Max reply nesting depth (1-10; 0 or unset = no limit)."`
}

type findSubredditsInput struct {
	Query string `json:"query,omitempty" jsonschema:"Topic or name to look for. Required unless sort is popular or new."`
	Sort  string `json:"sort,omitempty"  jsonschema:"search|popular|new. Default: search."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max results (1-100, default 25)."`
	After string `json:"after,omitempty" jsonschema:"Pagination cursor."`
}

type subredditInfoInput struct {
	Subreddit    string `json:"subreddit"               jsonschema:"Subreddit name (without /r/ prefix)."`
	IncludeRules bool   `json:"include_rules,omitempty" jsonschema:"Also fetch the posting rules. Costs one extra request."`
}

type wikiInput struct {
	Subreddit string `json:"subreddit"      jsonschema:"Subreddit name (without /r/ prefix)."`
	Page      string `json:"page,omitempty" jsonschema:"Wiki page path, e.g. 'index' or 'faq/posting'. Default: index."`
}

type usernameInput struct {
	Username string `json:"username" jsonschema:"Reddit username (without /u/ prefix)."`
}

type userPostsInput struct {
	Username   string `json:"username"              jsonschema:"Reddit username (without /u/ prefix)."`
	Sort       string `json:"sort,omitempty"        jsonschema:"new|top|hot|controversial. Default: new."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max posts (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type searchUsersInput struct {
	Query string `json:"query"           jsonschema:"Name or partial name to look for."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max results (1-100, default 25)."`
	After string `json:"after,omitempty" jsonschema:"Pagination cursor."`
}

// readOnly marks a tool as a side-effect-free read of an external system, so
// clients can auto-approve it instead of prompting on every call.
func readOnly(title string) *mcp.ToolAnnotations {
	openWorld := true

	return &mcp.ToolAnnotations{
		Title:         title,
		ReadOnlyHint:  true,
		OpenWorldHint: &openWorld,
	}
}

// text is the only shape a tool returns. The nil second value keeps the SDK
// from declaring an output schema or echoing a JSON copy of the result.
func text(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

func fail(tool string, err error) (*mcp.CallToolResult, any, error) {
	return nil, nil, fmt.Errorf("%s: %w", tool, err)
}

// Register adds every Reddit tool to the server. All of them are read-only.
func Register(server *mcp.Server, client *reddit.Client) {
	registerListingTools(server, client)
	registerPostTools(server, client)
	registerCommunityTools(server, client)
	registerUserTools(server, client)
}

// registerListingTools registers the feeds: posts and comment streams.
func registerListingTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browse_subreddit",
		Annotations: readOnly("Browse subreddit"),
		Description: "List posts from one subreddit, several at once (golang+rust), or the " +
			"site-wide 'all' and 'popular' feeds.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in browseInput) (*mcp.CallToolResult, any, error) {
		list, err := client.BrowseSubreddit(ctx, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return fail("browse_subreddit", err)
		}

		return text(renderPostList(fmt.Sprintf("r/%s · %s", in.Subreddit, orDefault(in.Sort, "hot")), list))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_reddit",
		Annotations: readOnly("Search Reddit"),
		Description: "Search posts site-wide, or within one or more subreddits. Reddit's search " +
			"operators (author:, flair:, site:, self:) work inside the query.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
		list, err := client.SearchReddit(ctx, in.Query, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return fail("search_reddit", err)
		}

		scope := "all of Reddit"
		if in.Subreddit != "" {
			scope = "r/" + in.Subreddit
		}

		return text(renderPostList(fmt.Sprintf("search %q in %s", in.Query, scope), list))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browse_comments",
		Annotations: readOnly("Browse comments"),
		Description: "Read a comment stream: every recent comment in a subreddit, or every recent " +
			"comment by a user. The subreddit stream shows what a community is saying without " +
			"having to pick threads first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in browseCommentsInput) (*mcp.CallToolResult, any, error) {
		list, err := client.BrowseComments(ctx, in.Subreddit, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return fail("browse_comments", err)
		}

		header := "comments in r/" + in.Subreddit
		if in.Username != "" {
			header = "comments by u/" + in.Username
		}

		return text(renderCommentList(header, list))
	})
}

// registerPostTools registers the tools that address specific posts and threads.
func registerPostTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_posts",
		Annotations: readOnly("Get posts"),
		Description: "Fetch posts by id, permalink or redd.it link, with full self-text. Pass up to " +
			"50 at once — the rate limit counts requests, not posts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postIDsInput) (*mcp.CallToolResult, any, error) {
		list, err := client.GetPosts(ctx, in.PostIDs)
		if err != nil {
			return fail("get_posts", err)
		}

		return text(renderPostList("posts by id", list))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_post_comments",
		Annotations: readOnly("Read thread"),
		Description: "Fetch a post with its comment tree, indented by reply depth. Reports when " +
			"Reddit collapsed replies, and expands one branch when given comment_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postCommentsInput) (*mcp.CallToolResult, any, error) {
		post, comments, err := client.GetPostComments(ctx, in.PostID, in.CommentID, in.CommentSort, in.Limit, in.Depth)
		if err != nil {
			return fail("get_post_comments", err)
		}

		return text(renderPostWithComments(post, comments))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_discussions",
		Annotations: readOnly("Find discussions of a link"),
		Description: "Find the Reddit threads about a link: pass a url to find every post " +
			"submitting it, or a post_id to find the other threads about that post's link. " +
			"Search does not reliably find these.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in discussionsInput) (*mcp.CallToolResult, any, error) {
		switch {
		case in.URL != "" && in.PostID != "":
			return fail("find_discussions", errBothTargets)
		case in.URL != "":
			list, err := client.FindPostsByURL(ctx, in.URL, in.Limit)
			if err != nil {
				return fail("find_discussions", err)
			}

			return text(renderPostList("posts linking to "+in.URL, list))
		case in.PostID != "":
			list, err := client.FindDuplicates(ctx, in.PostID, "", in.Limit)
			if err != nil {
				return fail("find_discussions", err)
			}

			return text(renderPostList("other threads about the same link as "+in.PostID, list))
		default:
			return fail("find_discussions", errNoTarget)
		}
	})
}

// registerCommunityTools registers subreddit discovery and documentation.
func registerCommunityTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_subreddits",
		Annotations: readOnly("Find subreddits"),
		Description: "Find communities by topic or name, or browse the popular and newly created " +
			"lists. Use this before browse_subreddit when the right community is not known.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findSubredditsInput) (*mcp.CallToolResult, any, error) {
		list, err := client.FindSubreddits(ctx, in.Query, in.Sort, in.After, in.Limit)
		if err != nil {
			return fail("find_subreddits", err)
		}

		header := fmt.Sprintf("subreddits matching %q", in.Query)
		if in.Query == "" {
			header = orDefault(in.Sort, "search") + " subreddits"
		}

		return text(renderSubredditList(header, list))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_subreddit_info",
		Annotations: readOnly("Subreddit info"),
		Description: "Fetch a subreddit's metadata (subscribers, active users, description, age, " +
			"type), and its posting rules when include_rules is set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in subredditInfoInput) (*mcp.CallToolResult, any, error) {
		sub, err := client.GetSubredditInfo(ctx, in.Subreddit, in.IncludeRules)
		if err != nil {
			return fail("get_subreddit_info", err)
		}

		return text(renderSubreddit(sub))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_wiki_page",
		Annotations: readOnly("Read subreddit wiki"),
		Description: "Read a page of a subreddit's wiki, where communities keep FAQs, guides and " +
			"recommendation lists. None of it appears in any listing. Defaults to the index page.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in wikiInput) (*mcp.CallToolResult, any, error) {
		page, err := client.GetWikiPage(ctx, in.Subreddit, in.Page)
		if err != nil {
			return fail("get_wiki_page", err)
		}

		return text(renderWikiPage(page))
	})
}

// registerUserTools registers profile and history reads.
func registerUserTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user",
		Annotations: readOnly("Get user profile"),
		Description: "Fetch a user's public profile: karma, account age, mod/gold flags, description.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in usernameInput) (*mcp.CallToolResult, any, error) {
		user, err := client.GetUser(ctx, in.Username)
		if err != nil {
			return fail("get_user", err)
		}

		return text(renderUser(user))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_posts",
		Annotations: readOnly("Get user posts"),
		Description: "Fetch submissions made by a user. For their comments, use browse_comments.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userPostsInput) (*mcp.CallToolResult, any, error) {
		list, err := client.GetUserPosts(ctx, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return fail("get_user_posts", err)
		}

		return text(renderPostList("posts by u/"+in.Username, list))
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_users",
		Annotations: readOnly("Find users"),
		Description: "Find Reddit accounts by name or partial name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchUsersInput) (*mcp.CallToolResult, any, error) {
		list, err := client.SearchUsers(ctx, in.Query, in.After, in.Limit)
		if err != nil {
			return fail("search_users", err)
		}

		return text(renderUserList(fmt.Sprintf("users matching %q", in.Query), list))
	})
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
