# registry

Runnable OCI registries assembled from
[`ohseeeye/oci`](https://github.com/ohseeeye/oci). Each configuration has its own
directory, `main.go`, and Dockerfile; HTTP lifecycle code lives in `internal/server`.

| Directory | Storage | Lifetime |
| --- | --- | --- |
| `sqlite/` | `ocisqlite`: SQLite metadata and files for content | Persists in the data directory |
| `mem/` | `ocimem`: everything in memory | Lost when the process exits |

Both serve plain HTTP without authentication. Local binaries bind to loopback
by default. Put a TLS and authentication proxy in front when exposing a registry
beyond local development.

## Run locally

From this repository's root, using Go 1.25 or newer:

```sh
go run ./sqlite -listen 127.0.0.1:5000 -data ./data
# Or, instead of SQLite:
go run ./mem -listen 127.0.0.1:5000
```

The SQLite backend creates its data directory. It contains `metadata.db`, blobs,
and upload sessions; preserve the entire directory. Memory storage needs no files.

```sh
curl http://127.0.0.1:5000/v2/
# {}
```

Both accept `-shutdown-timeout` (default `30s`). SIGINT and SIGTERM stop accepting
connections and let active requests finish before exiting. SQLite closes after
requests drain. If the deadline expires, connections are closed and the process
exits with an error. Whole-request timeouts are omitted to allow large transfers.

For reliable signal delivery outside development, build and run the binary directly:

```sh
mkdir -p bin
go build -o bin/registry-sqlite ./sqlite
./bin/registry-sqlite -data ./data
```

## Docker

Build with the repository root as the context:

```sh
docker build -f sqlite/Dockerfile -t ohseeeye/registry:dev-sqlite .
docker build -f mem/Dockerfile -t ohseeeye/registry:dev-mem .

docker volume create registry-data
docker run --rm --name registry --stop-timeout 35 \
  -p 127.0.0.1:5000:5000 -v registry-data:/data \
  ohseeeye/registry:dev-sqlite
# Or, instead of SQLite:
docker run --rm --name registry --stop-timeout 35 \
  -p 127.0.0.1:5000:5000 ohseeeye/registry:dev-mem
```

Images contain a static binary, run as UID/GID `65532:65532`, and listen on
`0.0.0.0:5000`. Named volumes inherit the image's writable `/data` permissions;
bind mounts must be writable by that UID. To override flags, pass the full flag
set after the image, including `-listen 0.0.0.0:5000` and `-data /data` for SQLite.
Docker's stop timeout should exceed `-shutdown-timeout` so it can finish draining.

## Optional pull-through cache configuration

Pass `-config /path/to/registry.yaml` to either binary. No config, an empty file,
or `upstreams: []` keeps the original empty, writable local registry. The config
is read once at startup; restart the process to apply changes or rotated secrets.
An example is in [`examples/registry.yaml`](examples/registry.yaml):

```yaml
version: 1
upstreams:
  - name: company
    match: ["company/**"]
    host: ghcr.io
    username: registry-reader
    password_file: /run/secrets/ghcr-password

  - name: dockerhub
    match: ["library/**"]
    host: registry-1.docker.io
```

Entries are checked in order; the first match wins. Globs match the entire
repository name using Go's [`path.Match`](https://pkg.go.dev/path#Match) syntax:
`*` matches within one path component, with `?` and character classes also
supported. Two small extensions cover subtrees: `team/**` matches `team` and all
its descendants, and `**` matches everything. Unmatched names use local storage.
Recursive `**` is supported only as the entire pattern or a final `/**`;
`team/**/image` and brace alternatives such as `{team,library}/**` are rejected.
Use separate patterns in `match` for alternatives.
Repository names pass upstream unchanged: pull `localhost:5000/library/alpine`,
for example. This initial config does not rewrite names or try another upstream
when the selected upstream returns an error.

Each upstream needs a unique `name`, one or more `match` patterns, and a `host`
(hostname or host:port, without a URL scheme or path). Omit credentials for
anonymous access, or supply both `username` and `password_file`. Passwords are
loaded from the mounted file into memory; a final LF or CRLF is removed, while
spaces are preserved. Relative secret paths resolve beside the YAML file.
Basic and bearer-token authentication use the OCI library's auth transport.
Unknown config fields, invalid globs, and unreadable or empty secrets fail startup.

Cache routes are read-only to downstream clients. The cache middleware normally
forwards writes upstream; this project wraps it with `ReadOnly` so a pull-through
route cannot mutate the source registry. Unmatched local repositories remain
writable. Upstream credentials do not authenticate downstream users; anyone who
can access this server can pull content available through those credentials.

Each upstream gets dedicated sparse-capable cache storage, separate from the
local registry and other upstreams. SQLite stores it under
`/data/caches/<name>-<identity>/`; memory caches disappear on exit. The identity
includes host, username, and HTTP/HTTPS selection so changing those settings
does not reuse another upstream's cache. Renaming an upstream creates a new
cache; old directories remain until removed manually while the server is stopped.

Optional per-upstream fields:

| Field | Default | Behavior |
| --- | --- | --- |
| `insecure` | `false` | Use plain HTTP for the upstream, useful for local testing; does not disable HTTPS certificate validation |
| `cache_tags` | `false` | Cache tag assignments indefinitely; there is no TTL in these storage backends |
| `fallback_tags_on_error` | `false` | Save successful tag assignments and use them on upstream network failures; does not fall back on HTTP errors or caller cancellation |

By default, tags resolve upstream on each lookup and verified manifest/blob
content is cached by digest. Listings always use the upstream. There is no
automatic cache expiration or size limit. Tag caching and fallback can serve
stale assignments indefinitely when enabled.

Mount the YAML and secret read-only, making the secret readable by container
UID `65532`. From the repository root, with `ghcr-password` supplied locally:

```sh
docker run --rm --name registry --stop-timeout 35 \
  -p 127.0.0.1:5000:5000 -v registry-data:/data \
  --mount type=bind,src="$PWD/examples/registry.yaml",dst=/etc/registry/config.yaml,readonly \
  --mount type=bind,src="$PWD/ghcr-password",dst=/run/secrets/ghcr-password,readonly \
  ohseeeye/registry:dev-sqlite \
  -listen 0.0.0.0:5000 -data /data -config /etc/registry/config.yaml
```

Images include system CA certificates and writable `/tmp` space for the cache's
temporary blob downloads. The cache implementation is `ocimiddleware.Cache`.
Its merge revision `b9b865f9ccf1` is pinned as a Go pseudo-version because the
newest published core tag, `v0.0.8`, does not contain it. SQLite uses `v0.0.3`.

## Versions and image tags

Use one Docker Hub image repository, `ohseeeye/registry`, and one project
version shared by every configuration. This version is independent of the OCI
library versions pinned in `go.mod`. Start at `v0.1.0`; bump the project version
when shipping changes to any configuration or shared code.

| Git release tag | Image tags |
| --- | --- |
| `v0.1.0` | `0.1.0-sqlite`, `0.1.0-mem` |
| `v0.1.1` | `0.1.1-sqlite`, `0.1.1-mem` |

`sqlite` and `mem` are moving aliases updated on every release. There is no bare
`latest` tag, so users always select a storage configuration. Treat versioned
tags as immutable; use a versioned tag or digest for deployments. Publish
releases in increasing version order so moving aliases keep pointing forward.
Additional configurations can use another suffix in the same image repository.

`.github/workflows/images.yml` tests and builds both Linux amd64/arm64 images on
pull requests and pushes to `main`. Separate Tests and Lint checks run race-enabled
Go tests, `gofmt` checks, and `go vet`; image builds wait for both checks to pass.
It publishes only on a stable `vX.Y.Z` tag,
to `docker.io/ohseeeye/registry`. Configure these GitHub Actions repository secrets:

- `DOCKERHUB_USERNAME`: the Docker Hub account used to push images.
- `DOCKERHUB_TOKEN`: a Docker Hub access token with push access to `ohseeeye/registry`.

Pull requests and pushes to `main` build without logging in or publishing.
Nothing is published by a local build. After pushing this project to its GitHub
repository, release with:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Development

```sh
go test -race ./...
go vet ./...
```

Dependencies use pinned module versions without sibling-directory replacements, so
the checkout and Docker contexts are self-contained.
