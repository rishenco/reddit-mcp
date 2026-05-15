package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rishenco/reddit-mcp/internal/reddit"
)

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

type postIDInput struct {
	PostID string `json:"post_id" jsonschema:"Reddit post ID (with or without 't3_' prefix)."`
}

type postCommentsInput struct {
	PostID      string `json:"post_id"                jsonschema:"Reddit post ID (with or without 't3_' prefix)."`
	CommentSort string `json:"comment_sort,omitempty" jsonschema:"best|top|new|controversial|old|qa. Default: best."`
	Limit       int    `json:"limit,omitempty"        jsonschema:"Max top-level comments (1-100, default 25)."`
	Depth       int    `json:"depth,omitempty"        jsonschema:"Max nesting depth (1-10, 0 = no limit)."`
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
	Post     reddit.Post      `json:"post"`
	Comments []reddit.Comment `json:"comments"`
}

func Register(server *mcp.Server, client *reddit.Client) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browse_subreddit",
		Description: "Fetch posts from a subreddit by sort order (hot/new/top/rising/controversial).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in browseInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.BrowseSubreddit(ctx, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("browse_subreddit: %w", err)
		}

		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_frontpage",
		Description: "Fetch posts from the Reddit frontpage (anonymous = r/popular; authenticated = personalized).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in frontpageInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.Frontpage(ctx, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_frontpage: %w", err)
		}

		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_reddit",
		Description: "Search Reddit posts site-wide, or scoped to a subreddit if subreddit is provided.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.SearchReddit(ctx, in.Query, in.Subreddit, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("search_reddit: %w", err)
		}

		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_post",
		Description: "Fetch a single Reddit post by ID (without its comment tree).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postIDInput) (*mcp.CallToolResult, *reddit.Post, error) {
		post, err := client.GetPost(ctx, in.PostID)
		if err != nil {
			return nil, nil, fmt.Errorf("get_post: %w", err)
		}

		return nil, post, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_post_comments",
		Description: "Fetch a Reddit post together with its threaded comment tree (flattened).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in postCommentsInput) (*mcp.CallToolResult, *postWithComments, error) {
		post, comments, err := client.GetPostComments(ctx, in.PostID, in.CommentSort, in.Limit, in.Depth)
		if err != nil {
			return nil, nil, fmt.Errorf("get_post_comments: %w", err)
		}

		return nil, &postWithComments{Post: *post, Comments: comments}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user",
		Description: "Fetch a Reddit user's public profile (karma, age, mod/gold flags, description).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in usernameInput) (*mcp.CallToolResult, *reddit.User, error) {
		user, err := client.GetUser(ctx, in.Username)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user: %w", err)
		}

		return nil, user, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_posts",
		Description: "Fetch submissions made by a Reddit user.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userListInput) (*mcp.CallToolResult, *reddit.PostList, error) {
		list, err := client.GetUserPosts(ctx, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user_posts: %w", err)
		}

		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_user_comments",
		Description: "Fetch a Reddit user's recent comments (flat list).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userListInput) (*mcp.CallToolResult, *reddit.CommentList, error) {
		list, err := client.GetUserComments(ctx, in.Username, in.Sort, in.TimeFilter, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_user_comments: %w", err)
		}

		return nil, list, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_subreddit_info",
		Description: "Fetch metadata for a subreddit (subscribers, active users, description, age, type).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in subredditInput) (*mcp.CallToolResult, *reddit.Subreddit, error) {
		sub, err := client.GetSubredditInfo(ctx, in.Subreddit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_subreddit_info: %w", err)
		}

		return nil, sub, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_trending_subreddits",
		Description: "Fetch currently popular subreddits.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listLimitInput) (*mcp.CallToolResult, *reddit.SubredditList, error) {
		list, err := client.TrendingSubreddits(ctx, in.After, in.Limit)
		if err != nil {
			return nil, nil, fmt.Errorf("get_trending_subreddits: %w", err)
		}

		return nil, list, nil
	})
}
