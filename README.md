# broken-mirror

A tiny **read-only git proxy**. Point `git` at `http://localhost:8080/OWNER/REPO`
and it transparently clones from GitHub, authenticating with your `gh` CLI token.
Every repo your token can read is available; pushes are refused unless the repo
is on an explicit allowlist.

## How it works

Git's smart-HTTP protocol has two services per repo: `git-upload-pack` (read:
clone/fetch) and `git-receive-pack` (write: push). `broken-mirror` reverse-proxies
to `github.com` and:

- **Defaults to read-only** — returns `403` for any `git-receive-pack` request
  unless the target repo is in `write_allow`.
- **Injects credentials** — GitHub rejects `Authorization: Bearer`, so the proxy
  sends HTTP Basic auth (`x-access-token:<token>`). The token only goes upstream,
  never to the git client.
- **Passes paths through** — any repo the token can reach works, no per-repo setup.

## Build & run

```sh
go build -o broken-mirror .
./broken-mirror                       # read-only, loopback :8080

git clone http://127.0.0.1:8080/OWNER/REPO   # clone anything your token can read
curl  http://127.0.0.1:8080/_repos           # list accessible repos
curl  http://127.0.0.1:8080/healthz          # liveness
```

## Configuration

Settings live in `broken-mirror.toml` (or `--config <path>`). CLI flags
(`--addr`, `--upstream`, `--token`) override the file.

```toml
addr     = "127.0.0.1:8080"
upstream = "https://github.com"
# token  = "ghp_xxx"          # optional; default: GH_TOKEN, GITHUB_TOKEN, then `gh auth token`

# Repos that may be READ. Glob patterns allowed ("*", "OWNER/*", "*/NAME").
# Empty/absent = every repo your token can reach (the default).
read_allow = [
  # "Tener/*",
]

# Repos that may be PUSHED TO. Everything else is read-only.
# Explicit "OWNER/REPO" only — no wildcards.
write_allow = [
  # "Tener/accresys",
]
```

- **`read_allow`** filters which repos can be cloned/fetched. It accepts glob
  patterns (`*` matches any characters including `/`, `?` matches one); matching
  is case-insensitive. Leave it empty to allow everything.
- **`write_allow`** is the only way to permit pushes. Entries are matched
  case-insensitively; wildcards, globs, and bare owners are rejected at startup.

The two are independent gates — reads check `read_allow`, pushes check
`write_allow`. If you narrow `read_allow`, make sure any writable repos are still
within it so they remain cloneable.

### Ref-level write policy

An optional `[write_policy]` table restricts *which refs* a push may update,
across all writable repos:

```toml
[write_policy]
branches = ["main", "release/*"]   # allowed branch names (after refs/heads/)
tags     = ["v*"]                  # allowed tag names (after refs/tags/)
```

A push is allowed only if **every** ref it updates matches — branch names against
`branches`, tag names against `tags` (globs, case-insensitive). An empty/omitted
list for a ref type denies that type (use `["*"]` to allow all); any other ref
namespace is denied. Omit the whole table for no ref-level restriction. The proxy
inspects the `git-receive-pack` request to enforce this and fails closed if it
can't parse the push.

## Security

- **Bind to loopback.** A public bind shares your token's read access with anyone
  who can reach the port.
- The proxy never forwards a client's `Authorization`; it always supplies its own.
- Keep `write_allow` as small as you mean it.
