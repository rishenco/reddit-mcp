package tools

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rishenco/reddit-mcp/internal/reddit"
)

// The MCP SDK, left to itself, puts the tool's JSON result in structuredContent
// and then repeats it verbatim as a text block, so every response crosses the
// wire twice. Filling Content with a compact rendering keeps the machine-readable
// half intact while giving the model something a good deal cheaper to read.

const (
	indentWidth  = 2
	maxIndent    = 10
	previewRunes = 220
)

func renderPostList(header string, list *reddit.PostList) string {
	var b strings.Builder

	writeHeader(&b, header, len(list.Posts), "post", list.DataSource, list.Note)

	for i, post := range list.Posts {
		writePostLine(&b, i+1, post)
	}

	writeCursor(&b, list.After)

	return b.String()
}

func writePostLine(b *strings.Builder, index int, post reddit.Post) {
	fmt.Fprintf(b, "\n%d. %s\n", index, oneLine(post.Title))
	fmt.Fprintf(b, "   %s · u/%s · %s\n", postStats(post), post.Author, formatTime(post.CreatedUTC))
	fmt.Fprintf(b, "   id=%s r/%s%s\n", post.ID, post.Subreddit, postFlags(post))
	fmt.Fprintf(b, "   %s\n", post.Permalink)

	if post.URL != "" && post.URL != post.Permalink {
		fmt.Fprintf(b, "   link: %s\n", post.URL)
	}

	if preview := oneLine(post.Selftext); preview != "" {
		fmt.Fprintf(b, "   %s\n", ellipsize(preview, previewRunes))
	}
}

func postStats(post reddit.Post) string {
	parts := make([]string, 0, 3)

	if post.Score != nil {
		parts = append(parts, strconv.Itoa(*post.Score)+" pts")
	}

	if post.UpvoteRatio != nil {
		parts = append(parts, fmt.Sprintf("%.0f%% up", *post.UpvoteRatio*100))
	}

	if post.NumComments != nil {
		parts = append(parts, strconv.Itoa(*post.NumComments)+" comments")
	}

	if len(parts) == 0 {
		return "no metrics (rss)"
	}

	return strings.Join(parts, " · ")
}

func postFlags(post reddit.Post) string {
	flags := make([]string, 0, 4)

	if post.Over18 != nil && *post.Over18 {
		flags = append(flags, "nsfw")
	}

	if post.Stickied {
		flags = append(flags, "stickied")
	}

	if post.Locked {
		flags = append(flags, "locked")
	}

	if post.LinkFlairText != "" {
		flags = append(flags, "flair:"+post.LinkFlairText)
	}

	if len(flags) == 0 {
		return ""
	}

	return " [" + strings.Join(flags, " ") + "]"
}

func renderPost(post *reddit.Post) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n", oneLine(post.Title))
	fmt.Fprintf(&b, "%s · u/%s · %s\n", postStats(*post), post.Author, formatTime(post.CreatedUTC))
	fmt.Fprintf(&b, "id=%s r/%s%s\n", post.ID, post.Subreddit, postFlags(*post))
	fmt.Fprintf(&b, "%s\n", post.Permalink)

	if post.URL != "" && post.URL != post.Permalink {
		fmt.Fprintf(&b, "link: %s\n", post.URL)
	}

	if post.Selftext != "" {
		fmt.Fprintf(&b, "\n%s\n", post.Selftext)

		if post.SelftextTruncated {
			b.WriteString("[self-text truncated]\n")
		}
	}

	return b.String()
}

func renderPostWithComments(post *reddit.Post, list *reddit.CommentList) string {
	var b strings.Builder

	b.WriteString(renderPost(post))

	if list.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", list.Note)
	}

	fmt.Fprintf(&b, "\n--- %d comments (source: %s) ---\n", len(list.Comments), list.DataSource)

	for _, comment := range list.Comments {
		writeComment(&b, comment)
	}

	writeMore(&b, list)

	return b.String()
}

func renderCommentList(header string, list *reddit.CommentList) string {
	var b strings.Builder

	writeHeader(&b, header, len(list.Comments), "comment", list.DataSource, list.Note)

	for _, comment := range list.Comments {
		writeComment(&b, comment)
	}

	writeMore(&b, list)
	writeCursor(&b, list.After)

	return b.String()
}

func writeComment(b *strings.Builder, comment reddit.Comment) {
	indent := strings.Repeat(" ", min(comment.Depth, maxIndent)*indentWidth)

	score := "no score (rss)"
	if comment.Score != nil {
		score = strconv.Itoa(*comment.Score) + " pts"
	}

	submitter := ""
	if comment.IsSubmitter {
		submitter = " [OP]"
	}

	fmt.Fprintf(b, "\n%su/%s%s · %s · %s · id=%s\n",
		indent, comment.Author, submitter, score, formatTime(comment.CreatedUTC), comment.ID)

	for _, line := range strings.Split(comment.Body, "\n") {
		fmt.Fprintf(b, "%s%s\n", indent, line)
	}

	if comment.BodyTruncated {
		fmt.Fprintf(b, "%s[truncated]\n", indent)
	}
}

func writeMore(b *strings.Builder, list *reddit.CommentList) {
	if list.MoreCount == 0 {
		return
	}

	fmt.Fprintf(b, "\n%d more replies not loaded.", list.MoreCount)

	if len(list.MoreParentIDs) > 0 {
		fmt.Fprintf(b, " Expand with get_post_comments(comment_id=…): %s",
			strings.Join(list.MoreParentIDs, ", "))
	}

	b.WriteString("\n")
}

func renderSubredditList(header string, list *reddit.SubredditList) string {
	var b strings.Builder

	writeHeader(&b, header, len(list.Subreddits), "subreddit", list.DataSource, list.Note)

	for i, sub := range list.Subreddits {
		fmt.Fprintf(&b, "\n%d. r/%s — %s\n", i+1, sub.Name, oneLine(sub.Title))

		if sub.Subscribers != nil {
			fmt.Fprintf(&b, "   %d subscribers", *sub.Subscribers)

			if sub.ActiveUsers != nil {
				fmt.Fprintf(&b, " · %d online", *sub.ActiveUsers)
			}

			b.WriteString("\n")
		}

		if desc := oneLine(sub.Description); desc != "" {
			fmt.Fprintf(&b, "   %s\n", ellipsize(desc, previewRunes))
		}
	}

	writeCursor(&b, list.After)

	return b.String()
}

func renderSubreddit(sub *reddit.Subreddit) string {
	var b strings.Builder

	fmt.Fprintf(&b, "r/%s — %s\n", sub.Name, oneLine(sub.Title))

	if sub.Subscribers != nil {
		fmt.Fprintf(&b, "%d subscribers", *sub.Subscribers)

		if sub.ActiveUsers != nil {
			fmt.Fprintf(&b, " · %d online", *sub.ActiveUsers)
		}

		b.WriteString("\n")
	} else {
		b.WriteString("subscriber and activity counts unavailable without credentials\n")
	}

	if sub.CreatedUTC > 0 {
		fmt.Fprintf(&b, "created %s", formatTime(sub.CreatedUTC))

		if sub.SubredditType != "" {
			fmt.Fprintf(&b, " · %s", sub.SubredditType)
		}

		b.WriteString("\n")
	}

	if sub.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", sub.Description)
	}

	fmt.Fprintf(&b, "%s\n", sub.URL)

	return b.String()
}

func renderUser(user *reddit.User) string {
	var b strings.Builder

	fmt.Fprintf(&b, "u/%s\n", user.Name)
	fmt.Fprintf(&b, "%d post karma · %d comment karma · joined %s\n",
		user.LinkKarma, user.CommentKarma, formatTime(user.CreatedUTC))

	flags := make([]string, 0, 3)
	if user.IsMod {
		flags = append(flags, "moderator")
	}

	if user.IsGold {
		flags = append(flags, "gold")
	}

	if user.IsEmployee {
		flags = append(flags, "reddit employee")
	}

	if len(flags) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(flags, " · "))
	}

	if user.PublicDescription != "" {
		fmt.Fprintf(&b, "\n%s\n", user.PublicDescription)
	}

	return b.String()
}

func writeHeader(b *strings.Builder, header string, count int, noun, source, note string) {
	fmt.Fprintf(b, "%s — %d %s", header, count, plural(noun, count))

	if source != "" {
		fmt.Fprintf(b, " (source: %s)", source)
	}

	b.WriteString("\n")

	if note != "" {
		fmt.Fprintf(b, "note: %s\n", note)
	}

	if count == 0 {
		b.WriteString("\nNo results.\n")
	}
}

func writeCursor(b *strings.Builder, after string) {
	if after != "" {
		fmt.Fprintf(b, "\nnext page: after=%s\n", after)
	}
}

func plural(noun string, count int) string {
	if count == 1 {
		return noun
	}

	return noun + "s"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func ellipsize(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	return strings.TrimRight(string(runes[:limit]), " ") + "…"
}

func formatTime(unix int64) string {
	if unix <= 0 {
		return "unknown date"
	}

	return time.Unix(unix, 0).UTC().Format(time.DateOnly)
}
