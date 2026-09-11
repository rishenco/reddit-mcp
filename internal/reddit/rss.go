package reddit

import (
	"encoding/xml"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
)

// Reddit's Atom feeds are the only logged-out surface that still answers, so
// they back every read the Data API refuses. What they give is thin: a title,
// an author, a permalink, a timestamp and the rendered body. There are no
// scores, comment counts, ratios or NSFW flags, which is why the fields they
// cannot fill stay absent rather than defaulting to zero.

type atomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	ID       string `xml:"id"`
	Title    string `xml:"title"`
	Updated  string `xml:"updated"`
	Content  string `xml:"content"`
	Category struct {
		Term  string `xml:"term,attr"`
		Label string `xml:"label,attr"`
	} `xml:"category"`
	Link struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Author struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

var (
	// selfTextRE isolates the post or comment body, which Reddit wraps in
	// SC_OFF/SC_ON markers inside the rendered entry content.
	selfTextRE = regexp.MustCompile(`(?s)<!-- SC_OFF -->(.*?)<!-- SC_ON -->`)
	// linkHrefRE finds the "[link]" anchor, which points at the submitted URL
	// for link posts and at the permalink for self posts.
	linkHrefRE = regexp.MustCompile(`<a href="([^"]+)"[^>]*>\s*\[link\]`)
	tagRE      = regexp.MustCompile(`<[^>]*>`)
	imgRE      = regexp.MustCompile(`<img[^>]*>`)
	anchorRE   = regexp.MustCompile(`(?s)<a\b[^>]*>.*?</a>`)
	spaceRE    = regexp.MustCompile(`[ \t]+`)
	// permalinkSubRE pulls the subreddit out of a permalink, the only place a
	// comment feed records it.
	permalinkSubRE = regexp.MustCompile(`/r/([^/]+)/`)
)

func parseFeed(body []byte) (*atomFeed, error) {
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("decode atom feed: %w", err)
	}

	return &feed, nil
}

func parseRSSPosts(body []byte, maxText int) (*PostList, error) {
	feed, err := parseFeed(body)
	if err != nil {
		return nil, err
	}

	out := &PostList{source: rssSource()}

	for _, entry := range feed.Entries {
		id, ok := strings.CutPrefix(entry.ID, "t3_")
		if !ok {
			continue
		}

		selftext, truncated := truncateText(postBody(entry.Content), maxText)
		permalink := entry.Link.Href

		target := permalink
		if m := linkHrefRE.FindStringSubmatch(entry.Content); m != nil {
			target = html.UnescapeString(m[1])
		}

		out.Posts = append(out.Posts, Post{
			ID:                id,
			Subreddit:         subredditFrom(entry, permalink),
			Title:             cleanText(entry.Title),
			Author:            strings.TrimPrefix(entry.Author.Name, "/u/"),
			URL:               target,
			Permalink:         permalink,
			CreatedUTC:        parseAtomTime(entry.Updated),
			IsSelf:            target == permalink,
			Selftext:          selftext,
			SelftextTruncated: truncated,
		})
	}

	return out, nil
}

func parseRSSComments(body []byte, maxText int) (*CommentList, error) {
	feed, err := parseFeed(body)
	if err != nil {
		return nil, err
	}

	out := &CommentList{source: rssSource()}

	for _, entry := range feed.Entries {
		id, ok := strings.CutPrefix(entry.ID, "t1_")
		if !ok {
			continue
		}

		text, truncated := truncateText(postBody(entry.Content), maxText)

		out.Comments = append(out.Comments, Comment{
			ID:            id,
			Author:        strings.TrimPrefix(entry.Author.Name, "/u/"),
			Body:          text,
			BodyTruncated: truncated,
			CreatedUTC:    parseAtomTime(entry.Updated),
			Permalink:     entry.Link.Href,
			Subreddit:     subredditFrom(entry, entry.Link.Href),
		})
	}

	return out, nil
}

func parseRSSSubreddits(body []byte) (*SubredditList, error) {
	feed, err := parseFeed(body)
	if err != nil {
		return nil, err
	}

	out := &SubredditList{source: rssSource()}

	for _, entry := range feed.Entries {
		if !strings.HasPrefix(entry.ID, "t5_") {
			continue
		}

		out.Subreddits = append(out.Subreddits, Subreddit{
			// The feed title is the subreddit's display title ("Ask Reddit..."),
			// so the addressable name has to come out of the link.
			Name:        subredditFromPermalink(entry.Link.Href),
			Title:       cleanText(entry.Title),
			Description: subredditDescription(entry.Content),
			CreatedUTC:  parseAtomTime(entry.Updated),
			URL:         entry.Link.Href,
		})
	}

	return out, nil
}

// postBody is the text of a post or comment. Reddit wraps the real body in
// SC_OFF/SC_ON markers and surrounds it with chrome — a thumbnail, "submitted
// by /u/x to r/y", and [link]/[comments] anchors. Anything outside the markers
// is that chrome, so a link post, which has no body at all, gets an empty one
// rather than a sentence about who submitted it.
func postBody(content string) string {
	m := selfTextRE.FindStringSubmatch(content)
	if m == nil {
		return ""
	}

	return cleanText(m[1])
}

// subredditDescription pulls the blurb out of a t5 feed entry, which has no
// SC_OFF markers: a thumbnail, the description text, and a [link] anchor.
func subredditDescription(content string) string {
	stripped := imgRE.ReplaceAllString(content, "")
	stripped = anchorRE.ReplaceAllString(stripped, "")

	return cleanText(stripped)
}

func cleanText(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spaceRE.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func subredditFrom(entry atomEntry, permalink string) string {
	// r/golang arrives as label="r/golang"; user feeds label the user instead,
	// so anything that is not an r/ label falls back to the permalink.
	if name, ok := strings.CutPrefix(entry.Category.Label, "r/"); ok {
		return strings.TrimSpace(name)
	}

	return subredditFromPermalink(permalink)
}

func subredditFromPermalink(permalink string) string {
	if m := permalinkSubRE.FindStringSubmatch(permalink); m != nil {
		return m[1]
	}

	return ""
}

func parseAtomTime(value string) int64 {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return 0
	}

	return t.Unix()
}
