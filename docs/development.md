# Development

Marquee is a Go program with a Svelte web page built into it. This page is the map: where things are, how the
parts are layered, how to run it while you work, and how to test and build it.

## What you need

- **Go 1.27** (see `go.mod`)
- **Node 24** and npm, only to build the web page
- Nothing else. There is no C compiler, no Docker and no database server involved in building or testing: SQLite is the
  pure-Go `modernc.org/sqlite`, and the build uses `CGO_ENABLED=0`.

On the maintainer's Windows machine, `. .\scripts\env.ps1` puts a portable Go and Node on the PATH of the current
PowerShell window only. Anywhere else, just have `go` and `node` on your PATH.

## Repository layout

```
cmd/marquee/       main.go and the wiring: builds each module and connects them (one file per area)
internal/          the program, one folder per module (see below)
  core/            config, database and migrations, job scheduler, outbound HTTP, secrets, events (SSE), path rules
web/               the Svelte 5 + Vite + TypeScript page; web/embed.go embeds web/dist into the binary
tools/precompress  brotli-compresses web/dist so the binary stays small
scripts/           build.ps1 and build.sh (release builds), dev.ps1, env.ps1, gen-api-doc.go
deploy/            Dockerfile and compose.yaml
docs/              these pages
releases/          release notes, one file per version: releases/0.1.0.md
.github/workflows/ ci.yml (every push and pull request) and release.yml (tags v*)
SPEC.md            what Marquee is meant to be, and the milestones
```

## Modules and layers

Each folder in `internal/` is one module. **A module imports only modules below it**, which keeps the program
understandable and stops cycles. The layers, from the bottom (`SPEC.md` has the full table):

| Layer | Modules | Job |
|---|---|---|
| 0 | `core/...` | the database (SQLite, migrations), the scheduler, the one outbound HTTP client, secret encryption, the event hub, path rules |
| 1 | `accounts`, `settings`, `metadata`, `parse` | people, sessions and keys; saved settings; TMDB, TVmaze, MusicBrainz and artwork; reading names of files and releases |
| 2 | `library`, `titles` | folders and file scanning; movies, shows, artists and what is wanted |
| 3 | `sources`, `decide`, `downloads`, `grab`, `search` | feeds; picking a release with reasons; download apps; finding and grabbing |
| 4 | `importer`, `mediaservers`, `history`, `watchlists` | finished download to library; Jellyfin, Emby, Plex; a title's history; Plex watchlists |
| 5 | `requests`, `calendar`, `alerts` | asks, approvals and limits; what airs when; messages to you |
| 6 | `mcp`, `web` | Claude's endpoint; the REST API, event stream, and the page |

The rules that matter most:

- **SQL lives only in `store*.go`.** Everything else asks the store.
- **Every call to an outside service goes through `core/outbound`.** It has timeouts, retries, a limit per host, a User-Agent
  and one debug log line per call, and it never logs a query string or a header. Do not use `http.Get`.
- **Secrets are sealed with `core/secrets`** and masked whenever they are shown. Never log or return one.
- **Migrations** are numbered SQL files in `internal/core/db/migrations/main` and `.../events`, embedded in the binary
  and run on start. Add the next number; never edit one that has shipped.
- **Handlers in `internal/web` call services**, with a guard that says who may use the route (`guarded(guard{...}, handler)`).
  The table in [api.md](api.md) is generated from those guards.
- **User-facing text is in the message catalog** (`web/src/lib/core/i18n/en.json`), not in components.
- **Plain words for people**: errors and reasons explain what happened, not an error code.

## Run it while you work

Backend only (the page is whatever is in `web/dist`):

```powershell
.\scripts\dev.ps1            # config in .dev\config, listening on 127.0.0.1:6464
```

`dev.ps1` makes a one-line placeholder page if `web\dist` does not exist yet, so the server always compiles. Extra
arguments pass through: `.\scripts\dev.ps1 -addr 127.0.0.1:6465`.

With hot reload for the page, run the server and the Vite dev server side by side:

```powershell
.\scripts\dev.ps1 -addr 127.0.0.1:6465      # terminal 1: the API
cd web; npm ci; npm run dev                  # terminal 2: the page, with /api proxied to the server
```

Vite's dev server proxies `/api` to `127.0.0.1:6465` (see `web/vite.config.ts`), so the server has to be on that port.
Open the address Vite prints (usually <http://localhost:5173>). The first visitor still becomes the owner, so
sign up in the dev copy; its data is in `.dev\config`, which Git ignores. Delete that folder to start over.

On Linux or macOS:

```sh
mkdir -p web/dist && echo '<!doctype html><title>Marquee</title>' > web/dist/index.html   # if you have not built the page
go run ./cmd/marquee -config .dev/config -addr 127.0.0.1:6465
```

## Test

```sh
go vet ./...
go test ./...                       # the whole backend; no network, no services needed
cd web && npm run check             # svelte-check, with warnings as errors
cd web && npm run build             # the production build
```

Go tests do not touch the network: providers, download apps and media servers are replaced by small local fakes in each
module's tests (`httptest` servers), so a test run works offline. Several modules keep a `testenv_test.go` for their fakes. CI
runs the same commands on every push ([`.github/workflows/ci.yml`](../.github/workflows/ci.yml)).

## Build a release

```powershell
.\scripts\build.ps1               # Windows: the web page, precompress, marquee.exe, a Linux binary and archive; prints sizes
.\scripts\build.ps1 -SkipUI       # reuse web\dist
```

```sh
scripts/build.sh                  # Linux/macOS/Git Bash: the page, precompress, five platforms, SHA256SUMS, a size table
scripts/build.sh --skip-ui
scripts/build.sh --targets "linux/amd64"
scripts/build.sh --check-size     # exit 3 if the linux/amd64 archive is over 17 MiB or marquee.exe is over 25 MiB
```

`build.sh` writes to `bin/` (override with `MARQUEE_OUT`). The version comes from the `VERSION` file, or from
`MARQUEE_VERSION`, which is how the release workflow stamps the tag. The size budgets are headline goals of the project:
**17 MiB** for the Linux archive and **25 MiB** for `marquee.exe`. If a change blows them, it needs a reason.

### Docker

Docker is not needed to work on Marquee. To build the image:

```sh
docker build -f deploy/Dockerfile --build-arg VERSION=$(cat VERSION) -t marquee .
docker run --rm -p 6464:6464 marquee
```

The Dockerfile builds the page in a Node stage, the binary in a Go stage (cross-compiling for the target, so no
emulation) and copies the result into an empty `scratch` image. CI builds and smoke-tests it for `linux/amd64` on every push.

### Cutting a release

1. Write `releases/<version>.md` (for example `releases/0.1.0.md`): the release notes. Without it, GitHub writes notes from
   the commits.
2. Tag and push: `git tag v0.1.0 && git push origin v0.1.0`.
3. The release workflow builds every archive, checks the size budget, runs `go vet` and `go test`, creates the GitHub
   release with the archives and `SHA256SUMS`, then builds and pushes `ghcr.io/baileypillon/marquee:<version>` (and `:latest`
   for a final version, not a pre-release like `0.2.0-rc.1`) for `amd64`, `arm64` and `arm/v7`.
4. The first time, open the package on GitHub (the repository's Packages) and make it public.

## Keeping the API docs current

[api.md](api.md) has a table of every route, made from the source:

```sh
go run scripts/gen-api-doc.go -write docs/api.md     # refresh the table
go run scripts/gen-api-doc.go -check docs/api.md     # exit 1 if it is stale
```

The program only reads `internal/web/*.go`; it imports nothing from Marquee. When you add a route it warns until the
route has a one-line description in the `purposes` table at the top of `scripts/gen-api-doc.go`.

## Licensing and where ideas come from

Marquee is MIT-licensed. It is inspired by [HolyArrs](https://github.com/HolyArrs/HolyArrs), which is AGPL-3.0, and it is a
clean-room implementation: **do not copy code, text or assets from HolyArrs** (or any AGPL or GPL project) into
this repository, and never call its servers. Ideas are free; code and wording are not.
