# reddit-mcp

An MCP server for Reddit: browse subreddits, search, read posts with their comment
trees, and look up users — over Reddit's public API, anonymously or with app-only
OAuth credentials.

It speaks two transports:

- **stdio** (default) — the MCP client spawns the binary and talks over stdin/stdout.
  This is what `.mcp.json` uses.
- **http** — a long-running streamable-HTTP server on `HTTP_ADDR`, endpoint `/mcp`.
  This is what `docker compose` runs.

Select one with `MCP_TRANSPORT=stdio|http` or the `--transport` flag (the flag wins).

## Running from `.mcp.json`

### Installed binary (recommended)

```sh
go install github.com/rishenco/reddit-mcp/cmd/reddit-mcp@latest   # → $(go env GOPATH)/bin/reddit-mcp
```

```json
{
  "mcpServers": {
    "reddit": {
      "type": "stdio",
      "command": "reddit-mcp",
      "env": {
        "REDDIT_CLIENT_ID": "your-client-id",
        "REDDIT_CLIENT_SECRET": "your-client-secret"
      }
    }
  }
}
```

Use the absolute path (`/Users/you/go/bin/reddit-mcp`) if `$GOPATH/bin` is not on the
`PATH` the MCP client inherits. Credentials are optional — drop the `env` block to run
in anonymous mode.

### From a checkout

The repo ships a project-scoped `.mcp.json` that builds and runs straight from source
with `go run`, so it works in a fresh clone without installing anything. It reads
`REDDIT_CLIENT_ID` / `REDDIT_CLIENT_SECRET` from the environment of the MCP client and
falls back to anonymous mode when they are unset.

```json
{
  "mcpServers": {
    "reddit": {
      "type": "stdio",
      "command": "go",
      "args": ["run", "./cmd/reddit-mcp", "--transport", "stdio"]
    }
  }
}
```

Or build once and point at the binary:

```sh
make build   # → bin/reddit-mcp
```

### Docker

```sh
docker build -t reddit-mcp:local .
```

```json
{
  "mcpServers": {
    "reddit": {
      "type": "stdio",
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "-e", "REDDIT_CLIENT_ID",
        "-e", "REDDIT_CLIENT_SECRET",
        "reddit-mcp:local"
      ]
    }
  }
}
```

`-i` is required: it keeps stdin open so the container can speak MCP.

### Connecting to the HTTP server instead

```sh
cp .env.example .env   # fill in credentials
make up                # docker compose, port 7137 by default
```

```json
{
  "mcpServers": {
    "reddit": {
      "type": "http",
      "url": "http://localhost:7137/mcp"
    }
  }
}
```

## Configuration

All settings come from the environment; see `.env.example`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `REDDIT_CLIENT_ID` | — | App-only OAuth client id. Optional. |
| `REDDIT_CLIENT_SECRET` | — | App-only OAuth secret. Must be set together with the id. |
| `REDDIT_USER_AGENT` | `reddit-mcp/0.1 (by /u/anonymous)` | Sent on every Reddit request. |
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http`. |
| `HTTP_ADDR` | `0.0.0.0:8080` | Bind address for the `http` transport. |
| `VERBOSE_LOG` | `false` | DEBUG-level logs with source locations. |
| `RATE_LIMIT_RPM` | auto | Override the limiter: 10 RPM anonymous, 60 RPM authenticated. |

Without credentials the server runs anonymously against `www.reddit.com` (~10 req/min).
With them it uses app-only OAuth against `oauth.reddit.com` (~60 req/min); create a
"script" app at <https://www.reddit.com/prefs/apps>.

Logs always go to stderr, so they never corrupt the MCP stream on stdout.

## Tools

| Tool | Description |
| --- | --- |
| `browse_subreddit` | Posts from a subreddit by sort order (hot/new/top/rising/controversial). |
| `get_frontpage` | Frontpage posts (r/popular anonymously, personalized when authenticated). |
| `search_reddit` | Search posts site-wide or within one subreddit. |
| `get_post` | A single post by id, without comments. |
| `get_post_comments` | A post plus its flattened comment tree. |
| `get_user` | A user's public profile. |
| `get_user_posts` | Submissions by a user. |
| `get_user_comments` | Recent comments by a user. |
| `get_subreddit_info` | Subreddit metadata. |
| `get_trending_subreddits` | Currently popular subreddits. |
