# reddit-mcp

A personal MCP server for reading Reddit: browse, search, read threads, follow a
link's discussions, and look up communities, wikis and users.

Built for two things: **complete read coverage** of Reddit, and **cheap
context** — the tool set costs ~8 KB in `tools/list`, results come back as
compact text rather than JSON, and every listing is one request.

## Credentials are not optional in practice

Reddit answers logged-out Data API requests with HTTP 403 from most networks —
datacenters and VPNs especially. The server still works without credentials, but
it falls back to Reddit's **public RSS feeds**, which carry far less:

| | Data API (credentials) | RSS fallback (anonymous) |
| --- | --- | --- |
| Posts, titles, bodies, permalinks | yes | yes |
| Score, upvote ratio, comment count | yes | **no** |
| NSFW / stickied / locked flags | yes | **no** |
| Threaded comments with depth | yes | flat, no scores |
| Profiles, subreddit stats, wikis, rules | yes | **no** |
| Link lookup and duplicates | yes | **no** |
| Pagination cursors | yes | **no** |
| Practical rate | ~100 req/min | ~10 req/min, throttled hard |

Results say which they are: a header marked `(rss)` is the fallback, and fields
the feeds cannot supply read as `metrics unknown` rather than zero, so the model
cannot mistake "unknown" for "no upvotes". Reads with no feed equivalent fail
with an error naming the environment variables to set.

To get the full API, create an app at <https://www.reddit.com/prefs/apps> and set
`REDDIT_CLIENT_ID` / `REDDIT_CLIENT_SECRET`. Since late 2025 new clients go
through a manual approval ticket rather than self-service, so plan for a wait.

## Running it

```sh
make build                        # → bin/reddit-mcp
make install                      # → $(go env GOPATH)/bin/reddit-mcp
```

```json
{
  "mcpServers": {
    "reddit": {
      "type": "stdio",
      "command": "/usr/local/bin/reddit-mcp",
      "env": {
        "REDDIT_CLIENT_ID": "your-client-id",
        "REDDIT_CLIENT_SECRET": "your-client-secret"
      }
    }
  }
}
```

The repo also ships a project-scoped `.mcp.json` that runs from a checkout with
`go run`, so a fresh clone works without installing anything.

### Long-running HTTP instead

```sh
cp .env.example .env   # fill in credentials
make up                # docker compose, port 7137 on loopback
```

```json
{ "mcpServers": { "reddit": { "type": "http", "url": "http://localhost:7137/mcp" } } }
```

`/mcp` has no authentication of its own, so `HTTP_ADDR` defaults to
`127.0.0.1:8080` and compose publishes the port on loopback. Before exposing it
anywhere else, set `MCP_AUTH_TOKEN`; clients then send
`Authorization: Bearer <token>` and `/healthz` stays open for probes.

## Tools

Twelve, all read-only and annotated as such so clients can auto-approve them.

| Tool | Reads |
| --- | --- |
| `browse_subreddit` | Posts from a subreddit, several at once (`golang+rust`), or the `all` / `popular` feeds. |
| `search_reddit` | Posts site-wide or within subreddits; Reddit's `author:` `flair:` `site:` `self:` operators work in the query. |
| `browse_comments` | Every recent comment in a subreddit, or by a user — the comment stream, without picking threads first. |
| `get_posts` | Up to 50 posts by id, permalink or `redd.it` link, with full self-text, in one request. |
| `get_post_comments` | A post with its comment tree, indented by depth; expands a collapsed branch with `comment_id`. |
| `find_discussions` | Threads about a link: by `url` (every post submitting it) or by `post_id` (other threads about the same link). |
| `find_subreddits` | Communities by topic or name, or the popular / newly created lists. |
| `get_subreddit_info` | Subreddit metadata, plus its posting rules with `include_rules`. |
| `get_wiki_page` | A subreddit wiki page — FAQs, guides and recommendation lists that appear in no listing. |
| `get_user` | A user's public profile. |
| `get_user_posts` | Submissions by a user. |
| `search_users` | Accounts by name or partial name. |

Anywhere a post is named, a base36 id, a `t3_` fullname, a full permalink, a
comment permalink and a `redd.it` link all work.

## How it stays cheap

Reddit's rate limit counts requests; the model's context counts bytes.

- **No output schemas, no JSON echo.** Declaring structured output would put a
  schema per tool in every `tools/list` and repeat the whole payload next to the
  rendering. On this tool set that was 9.7 KB of schema and roughly double the
  bytes per result, for data nothing reads back. Results are text.
- **Compact renderings.** A listing is one line of stats per post, not a JSON
  object with twenty fields.
- **Self-text is shortened in listings** (`LISTING_TEXT_CHARS`) and marked;
  `get_posts` and `get_post_comments` return it whole.
- **Responses are cached** in memory (LRU, `CACHE_MAX_MB`, TTL from `CACHE_TTL`),
  so re-reading a thread in a session costs nothing.
- **Batching everywhere it exists**: 50 ids per `get_posts`, 20 subreddits per
  `browse_subreddit`.
- **Truncation is reported.** A comment tree says how many replies Reddit
  collapsed and which branches to expand, instead of looking complete.

## Configuration

All settings come from the environment; see `.env.example`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `REDDIT_CLIENT_ID` | — | App-only OAuth client id. |
| `REDDIT_CLIENT_SECRET` | — | App-only OAuth secret. Must be set together with the id. |
| `REDDIT_USER_AGENT` | `go:github.com/rishenco/reddit-mcp:<version>` | Sent on every Reddit request. |
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http`; the `--transport` flag wins. |
| `HTTP_ADDR` | `127.0.0.1:8080` | Bind address for the `http` transport. |
| `MCP_AUTH_TOKEN` | — | Required bearer token for the `http` transport. |
| `CACHE_TTL` | `5m` | Base cache freshness; `0` disables caching. |
| `CACHE_MAX_MB` | `50` | Cache budget; `0` disables caching. |
| `LISTING_TEXT_CHARS` | `500` | Self-text cap inside listings; `0` disables it. |
| `COMMENT_TEXT_CHARS` | `0` | Comment body cap inside a thread; `0` keeps them whole. |
| `RATE_LIMIT_RPM` | auto | Override the limiter: 10 RPM anonymous, 100 RPM authenticated. |
| `VERBOSE_LOG` | `false` | DEBUG-level logs with source locations. |

Logs always go to stderr, so they never corrupt the MCP stream on stdout.

## Development

```sh
make test    # go test -race ./...
make lint    # golangci-lint run
```

CI runs build, vet, race tests and the linter on every push.
