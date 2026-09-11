package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rishenco/reddit-mcp/internal/reddit"
)

// Instructions is handed to the client at initialize time. It carries the
// things a model cannot discover from the schemas: which tool to reach for
// first, and that results can silently be the degraded RSS ones.
const Instructions = `Read-only access to Reddit.

Choosing a tool:
- Looking for a topic across Reddit: search_reddit. To find where a topic lives, search_subreddits first, then browse_subreddit.
- Reading a specific thread: get_post_comments. It takes a post id, a reddit.com permalink or a redd.it link.
- Several known posts at once: get_posts — one request instead of N against a tight rate limit.

Reading results:
- Every list carries data_source. "api" is complete; "rss" is the logged-out fallback and has no scores,
  comment counts, ratios or NSFW flags — absent fields there mean unknown, not zero. Say so if it matters.
- Listings truncate post self-text; get_post and get_post_comments return it whole.
- A comment tree reporting more_count is incomplete. Expand a branch with get_post_comments(comment_id=...).
- Post and comment ids are base36 and permalinks are absolute, so results can be cited directly.

Reddit rate-limits hard. Keep limit at its default unless the user asks for more, and prefer one
search over sweeping several subreddits.`

type browseInput struct {
	Subreddit  string `json:"subreddit"             jsonschema:"Subreddit name without /r/ prefix, e.g. 'golang'."`
	Sort       string `json:"sort,omitempty"        jsonschema:"Sort order: hot|new|top|rising|controversial. Default: hot."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"Applies to top/controversial only: hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max posts (1-100, default 25). Change ONLY if user explicitly asks."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor from a previous response."`
}

type frontpageInput struct {
	Sort       string `json:"sort,omitempty"        jsonschema:"hot|new|top|rising|controversial. Default: hot."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"For top/controversial only: hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max posts (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type searchInput struct {
	Query      string `json:"query"                 jsonschema:"Search query."`
	Subreddit  string `json:"subreddit,omitempty"   jsonschema:"If set, scope search to this subreddit."`
	Sort       string `json:"sort,omitempty"        jsonschema:"relevance|hot|new|top|comments. Default: relevance."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max results (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type searchSubredditsInput struct {
	Query string `json:"query"           jsonschema:"Topic or name to look for, e.g. 'rust programming'."`
	Limit int    `json:"limit,omitempty" jsonschema:"Max results (1-100, default 25)."`
	After string `json:"after,omitempty" jsonschema:"Pagination cursor."`
}

type postIDInput struct {
	PostID string `json:"post_id" jsonschema:"Post id (base36 e.g. '1wa14q6'), t3_ fullname, permalink or redd.it link."`
}

type postIDsInput struct {
	PostIDs []string `json:"post_ids" jsonschema:"Up to 50 post ids, permalinks or redd.it links; fetched in one request."`
}

type postCommentsInput struct {
	PostID      string `json:"post_id"                jsonschema:"Post id, t3_ fullname, reddit.com permalink or redd.it link."`
	CommentID   string `json:"comment_id,omitempty"   jsonschema:"Load this comment's subtree only. Use an id from more_parent_ids."`
	CommentSort string `json:"comment_sort,omitempty" jsonschema:"best|top|new|controversial|old|qa. Default: best."`
	Limit       int    `json:"limit,omitempty"        jsonschema:"Max comments for the whole thread, replies included (1-100, default 25)."`
	Depth       int    `json:"depth,omitempty"        jsonschema:"Max reply nesting depth (1-10; 0 or unset = no limit)."`
}

type usernameInput struct {
	Username string `json:"username" jsonschema:"Reddit username (without /u/ prefix)."`
}

type userListInput struct {
	Username   string `json:"username"              jsonschema:"Reddit username (without /u/ prefix)."`
	Sort       string `json:"sort,omitempty"        jsonschema:"new|top|hot|controversial. Default: new."`
	TimeFilter string `json:"time_filter,omitempty" jsonschema:"hour|day|week|month|year|all."`
	Limit      int    `json:"limit,omitempty"       jsonschema:"Max items (1-100, default 25)."`
	After      string `json:"after,omitempty"       jsonschema:"Pagination cursor."`
}

type subredditInput struct {
	Subreddit string `json:"subreddit" jsonschema:"Subreddit name (without /r/ prefix)."`
}

type listLimitInput struct {
	Limit int    `json:"limit,omitempty" jsonschema:"Max items (1-100, default 25)."`
	After string `json:"after,omitempty" jsonschema:"Pagination cursor."`
}

type postWithComments struct {
	Post     reddit.Post         `json:"post"`
	Comments *reddit.CommentList `json:"comments"`
}

// readOnly marks a tool as a safe, side-effect-free read of an external system,
// which lets clients auto-approve it instead of prompting for every call.
func readOnly(title string) *mcp.ToolAnnotations {
	openWorld := true

	return &mcp.ToolAnnotations{
		Title:         title,
		ReadOnlyHint:  true,
		OpenWorldHint: &openWorld,
	}
}

// text puts a compact rendering in the result's Content. Without it the SDK
// repeats the full JSON payload as text alongside structuredContent.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// Register adds every Reddit tool to the server. All of them are read-only.
func Register(server *mcp.Server, client *reddit.Client) {
	registerListingTools(server, client)
	registerPostTools(server, client)
	registerUserTools(server, client)
	registerCommunityTools(server, client)
}

// registerListingTools registers the tools for listings and search.
func registerListingTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browse_subreddit",
		Annotations: readOnly("Browse subreddit"),
		Description: "Fetch posts from a subreddit by sort order (hot/new/top/rising/controversial).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in browseInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.BrowseSubreddit(ctx, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("browse_subreddit: %w", err)
		}

		header := fmt.Sprintf("r/%s · %s", in.Subreddit, orDefault(in.Sort, "hot"))

		return text(renderPostList(header, list)), list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_frontpage",
		Annotations: readOnly("Reddit frontpage"),
		Description: "Fetch posts from the logged-out Reddit frontpage (the default popular feed). " +
			"App-only credentials have no user attached, so this is never personalized.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in frontpageInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.Frontpage(ctx, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_frontpage: %w", err)
		}

		return text(renderPostList("frontpage · "+orDefault(in.Sort, "hot"), list)), list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_reddit",
		Annotations: readOnly("Search Reddit"),
		Description: "Search Reddit posts site-wide, or scoped to a subreddit if subreddit is provided.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.SearchReddit(ctx, in.Query, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("search_reddit: %w", err)
		}

		scope := "all of Reddit"
		if in.Subreddit != "" {
			scope = "r/" + in.Subreddit
		}

		return text(renderPostList(fmt.Sprintf("search %q in %s", in.Query, scope), list)), list, nil
	})
}

// registerPostTools registers the tools for single posts and threads.
func registerPostTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_post",
		Annotations: readOnly("Get post"),
		Description: "Fetch a single Reddit post with its full self-text, without the comment tree.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postIDInput) (*mcp.CallToolResult, *reddit.Post, error) {
		post, err := client.GetPost(ctx, in.PostID)
		if err != nil {
			return nil, nil, fmt.Errorf("get_post: %w", err)
		}

		return text(renderPost(post)), post, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_posts",
		Annotations: readOnly("Get posts in bulk"),
		Description: "Fetch up to 50 posts in a single request. Prefer this over repeated get_post " +
			"calls — Reddit's rate limit counts requests, not posts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postIDsInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.GetPosts(ctx, in.PostIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("get_posts: %w", err)
		}

		return text(renderPostList("posts by id", list)), list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_post_comments",
		Annotations: readOnly("Read thread"),
		Description: "Fetch a Reddit post together with its comment tree (flattened, with depth and " +
			"parent_id). Reports more_count when Reddit collapsed replies.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postCommentsInput) (*mcp.CallToolResult, *postWithComments, error) {
		post, comments, err := client.GetPostComments(ctx, in.PostID, in.CommentID, in.CommentSort, in.Limit, in.Depth)
		if err != nil {
			return nil, nil, fmt.Errorf("get_post_comments: %w", err)
		}

		return text(renderPostWithComments(post, comments)), &postWithComments{Post: *post, Comments: comments}, nil
	})
}

// registerUserTools registers the tools for user profiles and history.
func registerUserTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user",
		Annotations: readOnly("Get user profile"),
		Description: "Fetch a Reddit user's public profile (karma, age, mod/gold flags, description). " +
			"Requires credentials: there is no logged-out fallback for profiles.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in usernameInput) (*mcp.CallToolResult, *reddit.User, error) {
		user, err := client.GetUser(ctx, in.Username)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user: %w", err)
		}

		return text(renderUser(user)), user, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_posts",
		Annotations: readOnly("Get user posts"),
		Description: "Fetch submissions made by a Reddit user.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userListInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.GetUserPosts(ctx, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user_posts: %w", err)
		}

		return text(renderPostList("posts by u/"+in.Username, list)), list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_comments",
		Annotations: readOnly("Get user comments"),
		Description: "Fetch a Reddit user's recent comments (flat list).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userListInput) (*mcp.CallToolResult, *reddit.CommentList, error) {
		list, err := client.GetUserComments(ctx, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user_comments: %w", err)
		}

		return text(renderCommentList("comments by u/"+in.Username, list)), list, nil
	})
}

// registerCommunityTools registers the tools for finding and describing subreddits.
func registerCommunityTools(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_subreddits",
		Annotations: readOnly("Find subreddits"),
		Description: "Find subreddits by name or topic. Use this before browse_subreddit when the " +
			"right community is not known.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchSubredditsInput) (*mcp.CallToolResult, *reddit.SubredditList, error) {
		list, err := client.SearchSubreddits(ctx, in.Query, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("search_subreddits: %w", err)
		}

		return text(renderSubredditList(fmt.Sprintf("subreddits matching %q", in.Query), list)), list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_subreddit_info",
		Annotations: readOnly("Subreddit info"),
		Description: "Fetch metadata for a subreddit (subscribers, active users, description, age, type).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in subredditInput) (*mcp.CallToolResult, *reddit.Subreddit, error) {
		sub, err := client.GetSubredditInfo(ctx, in.Subreddit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_subreddit_info: %w", err)
		}

		return text(renderSubreddit(sub)), sub, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_trending_subreddits",
		Annotations: readOnly("Trending subreddits"),
		Description: "Fetch currently popular subreddits.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listLimitInput) (*mcp.CallToolResult, *reddit.SubredditList, error) {
		list, err := client.TrendingSubreddits(ctx, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_trending_subreddits: %w", err)
		}

		return text(renderSubredditList("popular subreddits", list)), list, nil
	})
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
