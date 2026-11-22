# Configuration

Marquee has very little to configure outside the app. Three things are set when it starts (a flag or an
environment variable), and everything else is a setting you change in the web page.

## Flags and environment variables

| Flag | Environment variable | Default | What it does |
|---|---|---|---|
| `-addr` | `MARQUEE_ADDR` | `:6464` | Where to listen. `:6464` is every network interface; `127.0.0.1:6464` is this machine only. |
| `-config` | `MARQUEE_CONFIG` | `./config` | The folder that holds the databases and the master key. Made if it does not exist. |
| `-version` | | | Print the version and exit. |
| | `MARQUEE_MASTER_KEY_FILE` | `<config>/marquee.key` | Where the master key (the key that encrypts saved secrets) is kept. Made if it does not exist. |
| | `MARQUEE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Anything else means `info`. |
| | `MARQUEE_TRUSTED_PROXIES` | none | The reverse proxies whose `X-Forwarded-For` and `X-Forwarded-Proto` headers Marquee believes: addresses or CIDR ranges, separated by commas or spaces. See [reverse proxy](reverse-proxy.md#trusted-proxies-and-the-visitors-address). A value that is not an address stops Marquee from starting. |

A flag beats its environment variable, which beats the default. The Docker image sets `MARQUEE_CONFIG=/config`
and `MARQUEE_ADDR=:6464`.

Two standard variables matter too:

- `TZ` sets the time zone used in logs and dates (for example `TZ=Europe/Paris`). The program carries its own
  time zone database, so this works even in the Docker image.
- Marquee does **not** read `HTTP_PROXY`, `HTTPS_PROXY` or `NO_PROXY`. Its calls to outside services always go
  direct. There is no proxy setting yet.

There is nothing else: no setting for the sign-in session length, the database, TLS or a base path. Marquee
serves plain HTTP at the root of its address. To add HTTPS or a sub-domain, put it behind a
[reverse proxy](reverse-proxy.md). The address people use from outside (`publicUrl`) and the trusted proxies can
also be saved in Settings under the section named `server` (through the API for now); see the same page.

### The config folder

| File | What it is |
|---|---|
| `marquee.db` | State: people, titles, asks, sources, download apps, settings. (`-wal` and `-shm` files appear next to it while it runs.) |
| `marquee-events.db` | History and logs. |
| `marquee.key` | The master key: 32 random bytes. **Back it up** ([how](install.md#back-up-marqueekey)). |
| `cache/artwork/` | Posters and covers, kept for a week in the browser and capped at 500 MB on disk. Safe to delete. |

### Logging

Marquee writes plain text lines to its standard error: `docker logs marquee`, `journalctl -u marquee`, or the
console window on Windows. At `info` you see one line per request (never the query string, which can hold
tokens), one per Claude call and the important events. `debug` adds health checks, static files and every call
to an outside service (the address without the query, never a header or a key).

## What you set in the web page

Settings is for the owner. The tabs, in order:

| Tab | What goes there |
|---|---|
| **Connections** | Download apps, media servers, sources (feeds), watchlists and alert channels. See [connections](connections.md). |
| **Library folders** | The folders Marquee watches and puts finished downloads in, each marked movie, show or music. A 4K folder is any folder with `4k` or `uhd` in its name or label. |
| **Title info** | Your TMDB key; the status of TMDB, TVmaze and MusicBrainz. |
| **Quality rules** | Quality profiles: which resolutions and sources are allowed, the one you prefer, size limits (and size per hour), HDR, words to prefer or reject, minimum seeders. Every release Marquee considers gets a plain-words reason. |
| **People & limits** | Each person's weekly asks (HD and 4K) and whether their asks are approved automatically. The defaults for everyone. See [requests](requests.md). |
| **Subtitles** | Languages and providers for subtitles. See [connections](connections.md#subtitles). |
| **Claude & apps** | The Claude (MCP) switch, API keys and recent calls. See [Claude](claude-mcp.md). |
| **Language** | The language of the page. |
| **About** | The version, where the config folder is, how much disk is left, the state of the databases, the background jobs and every connection (the same as `GET /api/v1/system/status`, for the owner and admins). |

Secrets in settings (anything called a key, a token or a password) are encrypted with the master key before
they are saved. When you open a form they come back masked, like `••••1234`. Leave a masked value alone and the saved
secret is kept; clear the field to remove it.

### Things Marquee does on its own

| What | How often |
|---|---|
| Watch download apps for progress | every 5 seconds while something is downloading, every minute when idle |
| Look at the RSS feed of each source for new releases | every 15 minutes |
| Check Plex watchlists (if linked) | every 15 minutes |
| Look for finished downloads no one has imported | every 2 minutes |
| Search again for wanted titles that have no copy yet | every 6 hours |
| Scan library folders for new, changed and missing files | every 6 hours |
| Refresh details of titles you monitor | every 12 hours |
| Look for problems to report to your alert channels | every 5 minutes |
| Tidy up expired sessions and invites | every hour |

### Limits worth knowing

- A browser session lasts 30 days and slides forward while you use it. An invite link works for 7 days.
- Ten wrong sign-ins in 5 minutes from one address for one name pauses sign-in for that name and address.
- Claude: 60 calls a minute and 5 confirmed asks an hour per key ([details](claude-mcp.md#safety)).
- A JSON request to the API can be up to 1 MB (64 KB for one settings section).
- The calendar shows at most 92 days at a time.
