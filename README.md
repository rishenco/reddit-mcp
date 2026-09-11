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

## Installing

### Pre-built binary (recommended)

Every tagged release ships static binaries for linux, macOS and Windows on amd64 and
arm64 — no Go toolchain needed. Pick an archive from the
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

### From source

```sh
go install github.com/rishenco/reddit-mcp/cmd/reddit-mcp@latest   # → $(go env GOPATH)/bin/reddit-mcp
```

## Running from `.mcp.json`

Point the client at the installed binary:

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

Use an absolute path (`/usr/local/bin/reddit-mcp`, or `/Users/you/go/bin/reddit-mcp`
for `go install`) if the install location is not on the `PATH` the MCP client inherits.
Credentials are optional — drop the `env` block to run in anonymous mode.

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

## Releasing

Pushing a `v*` tag runs `.github/workflows/release.yml`, which cross-compiles every
platform, writes `checksums.txt`, and publishes them to a GitHub release with generated
notes:

```sh
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

The workflow can also be started by hand from the Actions tab for a tag that already
exists. The archives are built by `scripts/build-dist.sh`, so the same set can be
produced locally:

```sh
make dist VERSION=v0.1.0   # → dist/
```

The tag is stamped into the binary with `-ldflags -X main.version`, and is what
`reddit-mcp --version` and the MCP handshake report.

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
