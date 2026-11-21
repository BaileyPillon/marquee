# Marquee — build spec

Our own recreation of the idea in u/CoderLuii's r/ClaudeAI post "Claude Code is insane. My entire
self-hosted Netflix is 17 MB." (HolyArrs, github.com/HolyArrs/HolyArrs). One small self-hosted app that
does the jobs of Sonarr, Radarr, Lidarr, Bazarr, Prowlarr and Seerr, and plugs into what people already
run: qBittorrent / Deluge / SABnzbd to download, Jellyfin / Plex / Emby to watch.

- Started 2026-11-20 at Bailey's request ("recreate it end to end").
- **Approved look:** direction **A · Cinema** on the project's (private) design canvas. The canvas is
  the UI target. Progress is reported as target and current build side by side.
- **Clean-room rule:** HolyArrs is AGPL-3.0. Its docs (`D:\Projects\HolyArrs-reference\src\docs`) are a
  feature reference only. No code, strings or assets are copied from it. Its metadata server
  (api.holyarrs.com) is never called.

## Headline targets (what "recreated" means)

| Target | Measure |
|---|---|
| One program | a single static binary, no runtime dependencies, the UI embedded |
| Small | Linux amd64 release archive about 17 MB or less; Windows .exe under 25 MB |
| Replaces six apps | movies, TV, music, subtitles, sources and requests all work in it |
| Plugs into existing apps | qBittorrent, Deluge and SABnzbd for downloading; Jellyfin and Plex (Emby later) for watching |
| Requests | friends ask; 4K is a separate copy; every ask can wait for the owner's yes; per-person weekly limits |
| Watchlists | Plex friends' watchlists turn into asks on their own |
| Own metadata | works with the owner's TMDB key, plus keyless TVmaze and MusicBrainz; no third-party relay |
| Claude | an MCP server (Streamable HTTP), per-key permission ticks, Claude can ask but never approve |
| Languages | translated message catalogs (English first, then the rest by batch translation, 44 as the stretch goal) |
| Packaging | Docker image `FROM scratch`, plus Windows and Linux binaries |

## Non-goals

- No built-in scrapers for public torrent sites. Sources are Torznab/Newznab feeds the owner adds, or a
  linked Prowlarr/Jackett.
- No video playback. Jellyfin or Plex plays the files.
- No phone or TV apps. The web UI works at phone width.
- The end-to-end test library is public-domain films and Blender's Creative Commons open movies only.

## Stack

- **Backend:** Go (latest stable), standard-library `net/http` with the 1.22+ ServeMux patterns, `CGO_ENABLED=0`.
- **Database:** SQLite via `modernc.org/sqlite` (pure Go). Two files: `marquee.db` (state) and
  `marquee-events.db` (history and logs). Migrations are numbered SQL files embedded in the binary.
- **Frontend:** Svelte 5 + Vite + TypeScript, built as a static SPA, pre-compressed with brotli and
  embedded with `//go:embed`. Node is a build-time tool only.
- **Live updates:** Server-Sent Events (`/api/v1/events`) for download progress, new asks and health.
- **Build:** `-trimpath -ldflags "-s -w"`. Language catalogs are stored compressed and decompressed on request.

## Modules (backend `internal/<name>`, frontend `src/lib/<name>`)

A module imports only modules below it. SQL lives only in `store*.go`. Every outbound HTTP call goes
through `core/outbound`, which has timeouts, retries, a per-host rate limit and a log line per call.

| Layer | Module | Job |
|---|---|---|
| 0 | `core` | config and env, db and migrations, job scheduler, outbound HTTP client, secret encryption (master key file), logging, SSE hub |
| 1 | `accounts` | owner is the first visitor; local password, sessions, roles (owner, admin, member), API keys with scopes; Plex and Jellyfin sign-in later |
| 1 | `metadata` | TMDB (owner's key), TVmaze, MusicBrainz and Cover Art Archive; cache with expiry; artwork proxy |
| 2 | `library` | library folders, file scanner, filename parser (title, year, season/episode, quality, source, codec), matching files to titles |
| 2 | `titles` | movies, shows, seasons, episodes, artists, albums and tracks; the monitored and wanted state |
| 3 | `sources` | Torznab and Newznab clients, capability discovery, search, RSS polling, Prowlarr and Jackett bridges |
| 3 | `decide` | release-name parsing and quality rules (profiles: resolution, source, size limits, preferred words), with plain-words reasons for every pick |
| 3 | `downloads` | client adapters for qBittorrent (WebAPI v2), Deluge (JSON-RPC), SABnzbd (API), then Transmission and NZBGet; queue polling |
| 4 | `importer` | finished download → check → unpack (rar/7z/zip) → rename → hardlink or move → record; failure means a blocklist entry and a search again |
| 4 | `mediaservers` | Jellyfin and Emby library refresh; Plex section refresh; Plex watchlist reads (owner plus friends) |
| 4 | `subtitles` | OpenSubtitles.com (owner's key) and Podnapisi; scoring and language rules |
| 5 | `requests` | asks, quality choice (HD/4K), 4K as a separate copy with its own folder, approval rules, auto-approve per person, weekly limits, watchlist-to-ask sync |
| 5 | `calendar` | upcoming episodes and releases, expected dates |
| 5 | `alerts` | Discord webhook, ntfy, email (SMTP) |
| 6 | `mcp` | MCP Streamable HTTP at `/api/v1/mcp`, per-key permission ticks (See, Ask, Control downloads, Change settings, Delete, Admin, Approve), rate limits, a two-step confirm for any change |
| 6 | `web` | REST JSON `/api/v1/...`, SSE, static UI, auth middleware, CSRF |

### MCP tools (first set)

`search_titles`, `library_lookup`, `calendar`, `downloads` (read-only), `my_asks`, `health`,
`ask_for_title`. `ask_for_title` is a two-call confirm with a short-lived token, and it acts as the person
the key belongs to, under that person's limits. There is no approve, delete or settings tool unless the
key has that tick.

## Screens (from the approved canvas)

Home; Movie page; Show page; Artist page; Requests (owner); Ask sheet (phone); Approve (phone);
Friend's Home (phone); Downloads; Settings › Connections. Still to draw when we reach them: first-run
setup, Explore and search results, Calendar, Library grids, Quality rules, People & limits, and the rest
of Settings.

## Milestones

Each milestone ends with a check I run myself, not an agent's report.

| # | Milestone | Done when |
|---|---|---|
| M0 | Toolchain and skeleton | `go build` makes one binary that serves the embedded UI shell and `/api/v1/health`; the size is recorded |
| M1 | Accounts and settings | first visitor becomes owner; sign-in, sessions, roles; settings stored; the secret key file exists |
| M2 | Metadata and library | TMDB, TVmaze and MusicBrainz lookups cached; a test folder of open movies scans and matches correctly |
| M3 | Browsing UI | Home, Movie, Show and Artist pages match the canvas with real data; the side-by-side shots are posted |
| M4 | Requests | ask HD/4K, approval queue, limits, auto-approve, the 4K separate-copy folder; phone screens match the canvas |
| M5 | Find and download | Torznab/Newznab search, decide with reasons, send to qBittorrent/Deluge/SABnzbd, live progress |
| M6 | Import and rescan | rename, hardlink or move, Jellyfin rescans, the title shows "In library"; failure means search again |
| M7 | Watchlists, calendar, alerts | Plex friends' watchlists become asks; calendar; Discord/ntfy alerts |
| M8 | MCP | Claude Code connects with a key, searches, and asks for a title with confirm; it cannot approve |
| M9 | Subtitles and music | subtitles fetched by language rule; albums grabbed, tagged and imported |
| M10 | Languages | catalog plumbing plus batch-translated catalogs; a language switcher |
| M11 | Packaging | `FROM scratch` image, Windows and Linux binaries, size checked against the 17 MB target |
| E2E | Full run | on this PC: a local Torznab test feed of Blender open movies, qBittorrent or Deluge, and Jellyfin; ask, approve, download, import, rescan, then Claude asks over MCP |

## Machine notes (2026-11-20)

- Missing: Go, Docker, Jellyfin, Plex, qBittorrent (an old copy sits in `I:\Program Files`).
  Each install waits for Bailey's OK.
- Present: Node 24, npm, Python 3.14, git, gh, Deluge 2.1.1 (installed, not running), FFmpeg (`D:\Tools\FFmpeg`).
- C: is nearly full. The code, builds and the test library all live on D: (146 GB free) or M:.
