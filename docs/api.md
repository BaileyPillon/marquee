# API

Everything the web page does goes through a JSON API at `/api/v1`, and you can use it too: for scripts, for
other apps, or to see what the page is doing. Marquee is in beta, so details can still change between versions.

The endpoint table is [below](#endpoints). Every request and answer is JSON (`Content-Type: application/json`),
and a body can be up to 1 MB (64 KB for one settings section).

## Signing in

There are two ways to say who you are.

**An API key** is for scripts and other apps. Make one in **Settings → Claude & apps → New key** (see
[Claude and MCP](claude-mcp.md#make-a-key)) and send it on each request:

```sh
curl -H "Authorization: Bearer mq_..." https://marquee.example.com/api/v1/titles?kind=movie
```

A key acts as the person it belongs to, with only the permissions ticked on it. A key cannot make other keys,
and keys cannot be managed from the API at all: those endpoints need a browser sign-in.

**A browser session** is what the page uses: `POST /api/v1/auth/login` with `{"name": "...", "password": "..."}`
sets a cookie called `marquee_session` (HttpOnly, 30 days, renewed while you use it). Requests that change
anything (every method except GET, HEAD and OPTIONS) must also send the header **`X-Marquee: 1`**, which is what
stops other web sites from using your cookie. Requests with a Bearer key do not need it.

```sh
curl -c jar -H "X-Marquee: 1" -H "Content-Type: application/json" \
  -d '{"name":"bailey","password":"..."}' https://marquee.example.com/api/v1/auth/login
curl -b jar https://marquee.example.com/api/v1/me
```

### Who may do what

| Role | Permissions it can hold |
|---|---|
| Owner | `see`, `ask`, `downloads`, `settings`, `delete`, `admin`, `approve` |
| Admin | `see`, `ask`, `downloads`, `delete`, `admin`, `approve` |
| Member | `see`, `ask` |

A browser session holds every permission its role has. A key holds the ones ticked on it, never more than
the role. A route that lists a role (like "Owner only") needs that role **and**, for a key, the permission named
in the table. Some routes, such as owner-only settings, are narrower than the permission suggests: a key made by an
admin cannot have `settings` at all.

Further rules: members only see their own asks; secrets (API keys, passwords, tokens, webhook addresses) are
never returned, only masked like `••••1234`, and a masked value sent back unchanged keeps the stored secret.

## Answers and errors

Success is a `2xx` with a JSON body (`204` with no body for deletes). Errors have one shape:

```json
{ "error": { "code": "limit_reached", "message": "You have used all 2 of your 4K asks for this week." } }
```

`code` is stable and meant for programs; `message` is plain words for people. Some errors carry more fields next to
them (a used-up limit has `used`, `limit` and `resetsAt`). The statuses you will meet:

| Status | Meaning |
|---|---|
| `400` | The request is wrong (`invalid_json`, `invalid_request`, ...) |
| `401` | Not signed in, or a bad or revoked key (`unauthorized`, `invalid_api_key`) |
| `403` | Not allowed (`forbidden`, `insufficient_scope`, `session_required`, `csrf`) |
| `404` | No such thing (`not_found`); anything unknown under `/api/` is a JSON `404`, never the page |
| `409` | A conflict or something that needs setting up first (`already_requested`, `no_library_folder`, `tmdb_key_missing`, ...) |
| `429` | Too many sign-in attempts, or a weekly limit; has a `Retry-After` header |
| `502`, `504` | A service you connected (a download app, a source, Plex) did not answer |

Reads are never cached by browsers (`Cache-Control: no-store`), except artwork. Every response carries an
`X-Request-ID` (send your own, up to 64 letters, digits, `-` and `_`, and it is kept); it is the same id that
appears in the log.

## Live updates

`GET /api/v1/events` is a [Server-Sent Events](https://developer.mozilla.org/docs/Web/API/Server-sent_events) stream. Each
message has an `event:` name (the topic) and a JSON `data:`. Topics: `downloads`, `requests`, `titles`, `library`,
`library.scan` and `health`. Limit it with `?topic=downloads,requests`. A comment line is sent every 25 seconds
to keep the connection alive. A browser's `EventSource` cannot send a Bearer header; use `fetch` with a stream
reader, or a signed-in session.

Events are small notices, not full records: fetch the thing itself for details. Request events carry only an id and
a state, with no names or titles, because everyone who is signed in can read the stream.

## Example: ask for a movie

```sh
curl -X POST https://marquee.example.com/api/v1/requests \
  -H "Authorization: Bearer mq_..." -H "Content-Type: application/json" \
  -d '{"kind":"movie","provider":"tmdb","providerId":"19","quality":"uhd","note":"for Friday"}'
```

`quality` is `hd` or `uhd` (4K). The answer says whether the ask was approved on the spot. The same ask
follows the same [rules](requests.md) as one made in the page.

## Endpoints

This table is made from the source by `go run scripts/gen-api-doc.go -write docs/api.md`, so it follows the
code. "Who may call it" combines the role and, for an API key, the permission it must hold.

<!-- routes:start -->

_Generated by `go run scripts/gen-api-doc.go -write docs/api.md`. Do not edit between the markers._

### Sign-in, people and settings

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/health` | Anyone, no sign-in | Health check: status, version, uptime and database. |
| GET | `/api/v1/setup` | Anyone, no sign-in | Says whether first-run setup (creating the owner) is still needed. |
| POST | `/api/v1/setup/owner` | Anyone, no sign-in | First run: create the owner. Only works while there is no owner. |
| POST | `/api/v1/auth/login` | Anyone, no sign-in | Sign in with a name and password; sets the session cookie. |
| POST | `/api/v1/invites/accept` | Anyone, no sign-in | Accept an invite and create an account. |
| POST | `/api/v1/auth/logout` | Anyone, no sign-in | Sign out and clear the session cookie. |
| GET | `/api/v1/me` | Any signed-in person | Who you are, how you signed in and which permissions apply. |
| GET | `/api/v1/events` | Any signed-in person; a key needs `see` | Live updates as a Server-Sent Events stream (downloads, requests, library). |
| POST | `/api/v1/invites` | Owner or admin; a key needs `admin` | Create a one-time invite for a new admin or member. |
| GET | `/api/v1/apikeys` | Any signed-in person, browser sign-in only (not an API key) | List your API keys and the permissions you may put on one. |
| POST | `/api/v1/apikeys` | Any signed-in person, browser sign-in only (not an API key) | Create an API key. The secret is shown once. |
| DELETE | `/api/v1/apikeys/{id}` | Any signed-in person, browser sign-in only (not an API key) | Revoke an API key. |
| GET | `/api/v1/settings/{section}` | Owner only; a key needs `settings` | Read one settings section, with secrets masked. |
| PUT | `/api/v1/settings/{section}` | Owner only; a key needs `settings` | Replace one settings section; a masked secret keeps its stored value. |

### Search, titles and artwork

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/search` | Any signed-in person; a key needs `see` | Look up movies, shows and artists at the metadata providers. Saves nothing. |
| GET | `/api/v1/titles` | Any signed-in person; a key needs `see` | List tracked titles, with what is on disk for each. |
| POST | `/api/v1/titles` | Owner or admin; a key needs `admin` | Add a title to the catalog from a provider id. |
| GET | `/api/v1/titles/{id}` | Any signed-in person; a key needs `see` | One title with its episodes or albums and its files. |
| PATCH | `/api/v1/titles/{id}` | Owner or admin; a key needs `admin` | Turn monitoring on or off for a title. |
| DELETE | `/api/v1/titles/{id}` | Owner only; a key needs `delete` | Remove a title from the catalog. Never deletes files. |
| POST | `/api/v1/titles/{id}/refresh` | Owner or admin; a key needs `admin` | Fetch the title's details from the provider again. |
| GET | `/api/v1/metadata/status` | Any signed-in person; a key needs `see` | Whether a TMDB key is set, and how each provider last answered. |
| GET | `/api/v1/artwork/{provider}/{rest...}` | Any signed-in person; a key needs `see` | Posters, backdrops and covers, cached by Marquee (the only way the UI loads images). |

### Library folders and files

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/library/folders` | Any signed-in person; a key needs `see` | List library folders. |
| POST | `/api/v1/library/folders` | Owner only; a key needs `settings` | Add a library folder (movie, show or music). |
| DELETE | `/api/v1/library/folders/{id}` | Owner only; a key needs `settings` | Remove a library folder from Marquee. Files on disk are not touched. |
| POST | `/api/v1/library/scan` | Owner or admin; a key needs `admin` | Start scanning one folder or all of them; progress arrives on the event stream. |
| GET | `/api/v1/library/files` | Owner or admin; a key needs `admin` | List files the scanner found, by match state. |
| GET | `/api/v1/library/stats` | Any signed-in person; a key needs `see` | Counts and sizes for the library. |

### Library file actions

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| POST | `/api/v1/library/files/{id}/rename` | Owner or admin; a key needs `admin` | Give a file the name the importer would give it today. |
| DELETE | `/api/v1/library/files/{id}` | Owner only; a key needs `delete` | Take a file out of the library; deleteFile=true also deletes it from disk. |

### Downloads and download apps

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/downloads` | Owner or admin; a key needs `downloads` | List downloads (active, history or all). |
| GET | `/api/v1/downloads/summary` | Owner or admin; a key needs `downloads` | Counts of downloads by state, and download speed. |
| POST | `/api/v1/downloads/{id}/pause` | Owner or admin; a key needs `downloads` | Pause a download in its client. |
| POST | `/api/v1/downloads/{id}/resume` | Owner or admin; a key needs `downloads` | Resume a download in its client. |
| DELETE | `/api/v1/downloads/{id}` | Owner or admin; a key needs `downloads` | Remove a download from its client (optionally with its data). |
| GET | `/api/v1/clients` | Owner only; a key needs `settings` | List download apps (qBittorrent, Deluge, SABnzbd); secrets masked. |
| POST | `/api/v1/clients` | Owner only; a key needs `settings` | Add a download app. |
| PUT | `/api/v1/clients/{id}` | Owner only; a key needs `settings` | Change a download app; fields left out keep their value. |
| DELETE | `/api/v1/clients/{id}` | Owner only; a key needs `settings` | Remove a download app. |
| POST | `/api/v1/clients/{id}/test` | Owner only; a key needs `settings` | Test the connection to a download app. |

### Requests, people and limits

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| POST | `/api/v1/requests` | Any signed-in person; a key needs `ask` | Ask for a title in HD or 4K, as the caller. |
| GET | `/api/v1/requests` | Any signed-in person; a key needs `see` | List asks. Members only ever see their own. |
| GET | `/api/v1/requests/summary` | Any signed-in person; a key needs `see` | Counts of asks by state. |
| GET | `/api/v1/requests/{id}` | Any signed-in person; a key needs `see` | One ask. |
| POST | `/api/v1/requests/{id}/approve` | Owner or admin; a key needs `approve` | Approve an ask (optionally always allow that person 4K). |
| POST | `/api/v1/requests/{id}/decline` | Owner or admin; a key needs `approve` | Decline an ask, with an optional reason. |
| DELETE | `/api/v1/requests/{id}` | Any signed-in person; a key needs `ask` | Cancel your own ask. |
| GET | `/api/v1/people` | Owner or admin; a key needs `admin` | People with their weekly use and limits. |
| PUT | `/api/v1/people/{userId}/limits` | Owner only; a key needs `settings` | Set a person's weekly limits and auto-approve rules. |

### Sources, quality rules and release search

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/sources` | Owner only; a key needs `settings` | List sources (Torznab and Newznab feeds); keys masked. |
| POST | `/api/v1/sources` | Owner only; a key needs `settings` | Add a source. |
| POST | `/api/v1/sources/import` | Owner only; a key needs `settings` | Import every indexer from a Prowlarr or Jackett. |
| PUT | `/api/v1/sources/{id}` | Owner only; a key needs `settings` | Change a source; fields left out keep their value. |
| DELETE | `/api/v1/sources/{id}` | Owner only; a key needs `settings` | Remove a source. |
| POST | `/api/v1/sources/{id}/test` | Owner only; a key needs `settings` | Test a source and read what it supports. |
| GET | `/api/v1/profiles` | Owner only; a key needs `settings` | List quality profiles. |
| PUT | `/api/v1/profiles/{id}` | Owner only; a key needs `settings` | Change a quality profile. |
| GET | `/api/v1/blocklist` | Owner only; a key needs `settings` | List releases Marquee will not pick again. |
| DELETE | `/api/v1/blocklist/{id}` | Owner only; a key needs `settings` | Take a release off the blocklist. |
| GET | `/api/v1/titles/{id}/releases` | Owner or admin; a key needs `admin` | Search all sources now and rank the releases, with reasons. |
| POST | `/api/v1/titles/{id}/grab` | Owner or admin; a key needs `downloads` | Send one chosen release to a download app. |

### Import and history

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/titles/{id}/history` | Any signed-in person; a key needs `see` | A title's history, newest first. |
| GET | `/api/v1/titles/{id}/open` | Any signed-in person; a key needs `see` | The address to open the title on a media server. |
| POST | `/api/v1/downloads/{id}/import` | Owner or admin; a key needs `downloads` | Run the import again for a finished or failed download. |
| GET | `/api/v1/import/status` | Owner or admin; a key needs `downloads` | The importer's last 20 results and what it is working on. |

### Media servers

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/mediaservers` | Owner only; a key needs `settings` | List media servers (Jellyfin, Emby, Plex); tokens masked. |
| POST | `/api/v1/mediaservers` | Owner only; a key needs `settings` | Add a media server. |
| PUT | `/api/v1/mediaservers/{id}` | Owner only; a key needs `settings` | Change a media server; fields left out keep their value. |
| DELETE | `/api/v1/mediaservers/{id}` | Owner only; a key needs `settings` | Remove a media server. |
| POST | `/api/v1/mediaservers/{id}/test` | Owner only; a key needs `settings` | Test a media server and list its libraries. |

### Plex watchlists

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/watchlists` | Owner only; a key needs `settings` | Plex link status, friends and the last check. |
| PUT | `/api/v1/watchlists/plex` | Owner only; a key needs `settings` | Change how Plex watchlists are read: on or off, friends, auto-created members, quality. |
| POST | `/api/v1/watchlists/plex/link` | Owner only; a key needs `settings` | Start linking the owner's Plex account (a PIN). |
| GET | `/api/v1/watchlists/plex/link/{pinId}` | Owner only; a key needs `settings` | Check whether the Plex link was approved. |
| DELETE | `/api/v1/watchlists/plex/link` | Owner only; a key needs `settings` | Unlink the Plex account. |
| PUT | `/api/v1/watchlists/plex/friends/{plexUserId}` | Owner only; a key needs `settings` | Link or unlink a Plex friend and a Marquee person. |
| POST | `/api/v1/watchlists/check` | Owner only; a key needs `settings` | Check the Plex watchlists now. |

### Calendar

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/calendar` | Any signed-in person; a key needs `see` | Upcoming episodes and releases (at most 92 days at a time). |

### Alerts

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/alerts` | Owner only; a key needs `settings` | List alert channels; secrets masked. |
| POST | `/api/v1/alerts` | Owner only; a key needs `settings` | Add an alert channel (Discord, ntfy, email and others). |
| PUT | `/api/v1/alerts/{id}` | Owner only; a key needs `settings` | Change an alert channel. |
| DELETE | `/api/v1/alerts/{id}` | Owner only; a key needs `settings` | Remove an alert channel. |
| POST | `/api/v1/alerts/{id}/test` | Owner only; a key needs `settings` | Send a test message to a channel. |

### Claude (MCP)

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| ANY | `/mcp` | Bearer API key only (no browser session) | The MCP endpoint (Streamable HTTP, JSON-RPC). Bearer API key only. |
| ANY | `/api/v1/mcp` | Bearer API key only (no browser session) | Same endpoint under the API prefix. |
| GET | `/api/v1/mcp/info` | Any signed-in person | Whether Claude's endpoint is on, its address, its tools and recent calls. |

### System status

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/system/status` | Owner or admin; a key needs `admin` | How Marquee is doing: version, config folder, databases, disk, jobs and connections (the About screen). |

### Music

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| POST | `/api/v1/titles/{id}/albums/{albumId}/search` | Owner or admin; a key needs `downloads` | Album search (undocumented) |

### Subtitles

| Method | Path | Who may call it | What it does |
|---|---|---|---|
| GET | `/api/v1/subtitles/status` | Owner or admin; a key needs `admin` | Subtitle status (undocumented) |
| POST | `/api/v1/subtitles/test` | Owner only; a key needs `settings` | Subtitle test (undocumented) |
| DELETE | `/api/v1/subtitles/{id}` | Owner or admin; a key needs `admin` | Subtitle delete (undocumented) |
| GET | `/api/v1/titles/{id}/subtitles` | Any signed-in person; a key needs `see` | Title subtitles (undocumented) |
| GET | `/api/v1/library/files/{id}/subtitles` | Any signed-in person; a key needs `see` | File subtitles (undocumented) |
| POST | `/api/v1/library/files/{id}/subtitles/search` | Owner or admin; a key needs `admin` | File subtitle search (undocumented) |
| POST | `/api/v1/library/files/{id}/subtitles/download` | Owner or admin; a key needs `admin` | File subtitle download (undocumented) |

<!-- routes:end -->

Besides these, the web page itself is served from `/` (any path that is not a file falls back to the page, so
links like `/requests` work when reloaded), with fingerprinted files under `/assets/`.
