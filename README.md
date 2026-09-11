# reddit-mcp

An MCP server for Reddit: browse subreddits, search, read threads with their
comment trees, and look up users.

## Credentials are not optional in practice

Reddit answers logged-out Data API requests with HTTP 403 from most networks —
datacenters and VPNs especially — and has done since mid-2026. The server still
works without credentials, but it falls back to Reddit's **public RSS feeds**,
which carry far less:

| | Data API (credentials) | RSS fallback (anonymous) |
| --- | --- | --- |
| Posts, titles, bodies, permalinks | yes | yes |
| Score, upvote ratio, comment count | yes | **no** |
| NSFW / stickied / locked flags | yes | **no** |
| Threaded comments with depth | yes | flat, no scores |
| User profiles, subreddit stats | yes | **no** |
| Pagination cursors | yes | **no** |
| Practical rate | ~100 req/min | ~10 req/min, throttled hard |

Every response says which it is. Lists carry `data_source: "api"` or
`"rss"`, and fields the RSS feeds cannot supply are **absent rather than zero**,
so a model reading the result cannot mistake "unknown" for "no upvotes".

To get the full API: create an app at <https://www.reddit.com/prefs/apps> and set
`REDDIT_CLIENT_ID` / `REDDIT_CLIENT_SECRET`. Since late 2025 new clients go
through a manual approval ticket rather than self-service, so plan for a wait.
Free access is non-commercial only.

## Installing

### Container (recommended)

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
        "ghcr.io/rishenco/reddit-mcp:latest"
      ]
    }
  }
}
```

`-i` is required: it keeps stdin open so the container can speak MCP.

The server is also listed in the [MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.rishenco/reddit-mcp`.

### Pre-built binary

Every tagged release ships static binaries for linux, macOS and Windows on amd64
and arm64 — no Go toolchain needed. Pick an archive from the
[latest release](https://github.com/rishenco/reddit-mcp/releases/latest), or fetch the
one for your platform:

```sh
VERSION=0.1.0
OS=$(uname -s | tr '[:upper:]' '[:lower:]')                 # linux or darwin
ARCH=$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')  # amd64 or arm64

curl -fsSL -o reddit-mcp.tar.gz \
  "https://github.com/rishenco/reddit-mcp/releases/download/v$VERSION/reddit-mcp_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf reddit-mcp.tar.gz                                  # → ./reddit-mcp
sudo install reddit-mcp /usr/local/bin/reddit-mcp
reddit-mcp --version
```

Windows builds are `.zip` instead. Each release also carries a `checksums.txt`, so the
download can be verified before it is installed:

```sh
curl -fsSLO "https://github.com/rishenco/reddit-mcp/releases/download/v$VERSION/checksums.txt"
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 -c checksums.txt
```

Then point the client at it:

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

Use an absolute path if the install location is not on the `PATH` the MCP client
inherits.

### From source

```sh
go install github.com/rishenco/reddit-mcp/cmd/reddit-mcp@latest   # → $(go env GOPATH)/bin/reddit-mcp
```

The repo also ships a project-scoped `.mcp.json` that runs straight from a
checkout with `go run`, so it works in a fresh clone without installing
anything. `make build` produces `bin/reddit-mcp` instead.

### HTTP transport

```sh
cp .env.example .env   # fill in credentials
make up                # docker compose, port 7137 on loopback
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

`/mcp` has **no authentication of its own**, so `HTTP_ADDR` defaults to
`127.0.0.1:8080` and compose publishes the port on loopback. Before exposing it
anywhere else, set `MCP_AUTH_TOKEN` — otherwise anyone who can reach the port
spends your Reddit quota. With a token set, clients send
`Authorization: Bearer <token>`; `/healthz` stays open for probes.

## Tools

All twelve are read-only and annotated as such, so clients can auto-approve them.

| Tool | Description |
| --- | --- |
| `browse_subreddit` | Posts from a subreddit by sort order (hot/new/top/rising/controversial). |
| `get_frontpage` | Logged-out frontpage posts. App-only credentials have no user, so this is never personalized. |
| `search_reddit` | Search posts site-wide or within one subreddit. |
| `search_subreddits` | Find communities by name or topic — the step before browsing. |
| `get_post` | A single post with its full self-text, without comments. |
| `get_posts` | Up to 50 posts in one request. |
| `get_post_comments` | A post plus its flattened comment tree; expands a branch with `comment_id`. |
| `get_user` | A user's public profile. Needs credentials. |
| `get_user_posts` | Submissions by a user. |
| `get_user_comments` | Recent comments by a user. |
| `get_subreddit_info` | Subreddit metadata. |
| `get_trending_subreddits` | Currently popular subreddits. |

Ids, permalinks and URLs are interchangeable wherever a post is named:
`1wa14q6`, `t3_1wa14q6`, a full `reddit.com/r/.../comments/...` link (including a
comment permalink) and a `redd.it` short link all resolve to the same post.

### Keeping responses small

Reddit's rate limit counts requests and the model's context counts bytes, so:

- Listings truncate post self-text to `LISTING_TEXT_CHARS` and flag it with
  `selftext_truncated`. `get_post` and `get_post_comments` return whole text.
- Responses are cached in memory (LRU, `CACHE_MAX_MB`, TTL from `CACHE_TTL`),
  so re-reading the same thread in a session costs nothing.
- `get_posts` batches ids into a single `/by_id` call.
- Tool results carry a compact text rendering alongside the structured JSON
  rather than the same JSON twice.
- Comment trees report `more_count` and `more_parent_ids` when Reddit collapsed
  replies, instead of looking complete while silently missing branches.

## Configuration

All settings come from the environment; see `.env.example`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `REDDIT_CLIENT_ID` | — | App-only OAuth client id. |
| `REDDIT_CLIENT_SECRET` | — | App-only OAuth secret. Must be set together with the id. |
| `REDDIT_USER_AGENT` | `go:github.com/rishenco/reddit-mcp:<version>` | Sent on every Reddit request. |
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http`. |
| `HTTP_ADDR` | `127.0.0.1:8080` | Bind address for the `http` transport. |
| `MCP_AUTH_TOKEN` | — | Required bearer token for the `http` transport. |
| `CACHE_TTL` | `5m` | Base cache freshness; `0` disables caching. |
| `CACHE_MAX_MB` | `50` | Cache budget; `0` disables caching. |
| `LISTING_TEXT_CHARS` | `500` | Self-text cap inside listings; `0` disables it. |
| `RATE_LIMIT_RPM` | auto | Override the limiter: 10 RPM anonymous, 100 RPM authenticated. |
| `VERBOSE_LOG` | `false` | DEBUG-level logs with source locations. |

Select the transport with `MCP_TRANSPORT` or the `--transport` flag (the flag wins).
Logs always go to stderr, so they never corrupt the MCP stream on stdout.

## Development

```sh
make test    # go test -race ./...
make lint    # golangci-lint run
make build   # → bin/reddit-mcp
```

CI runs build, vet, race tests and the linter on every push.

## Releasing

Pushing a `v*` tag runs `.github/workflows/release.yml`, which:

1. cross-compiles every platform and publishes a GitHub release with
   `checksums.txt` (`make dist VERSION=v0.1.0` reproduces this locally);
2. builds and pushes a multi-arch image to `ghcr.io/rishenco/reddit-mcp`;
3. publishes `server.json` to the MCP Registry over GitHub OIDC.

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow can also be started by hand from the Actions tab for a tag that
already exists. The tag is stamped into the binary with
`-ldflags -X main.version`, and is what `reddit-mcp --version`, the MCP
handshake and the default User-Agent report.
