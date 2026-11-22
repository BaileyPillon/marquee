# Claude and MCP

Marquee has an [MCP](https://modelcontextprotocol.io) server built in, so Claude Code (or another MCP client) can
search your library, look at what is on disk and downloading, and **ask** for titles, as you, in plain
conversation: "is Metropolis in the library? ask for it in 4K".

The important rule: **Claude can ask, but it can never approve, decline, delete or change settings.** There are
no tools for those, whatever permissions you give the key. The owner answers asks inside Marquee, as always.

## Turn it on

It is **on by default**: there is nothing to switch on, and nobody gets in without a key. Open
**Settings → Claude & apps** to see the address, the exact command to run, the tools and recent calls.

To turn the endpoint off (every key then stops working until you turn it back on), send an owner request:

```sh
curl -X PUT https://marquee.example.com/api/v1/settings/mcp \
  -H "Authorization: Bearer <an owner key with the settings permission>" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

If your Marquee is reached by a different name than you use in your browser, and a browser-based MCP client
needs it, list that page's origin in the same section: `{"enabled": true, "allowedOrigins": ["https://my-client.example.com"]}`.
Clients that are not in a browser (Claude Code, scripts) send no `Origin` header and need nothing here.

## Make a key

1. **Settings → Claude & apps → New key.** Name it for where it will be used ("Bailey's laptop").
2. Tick only what it needs. For Claude, that is usually **See** and **Ask**.
3. Copy the key when it is shown. **It is shown once**; Marquee keeps only a hash. Keys start with `mq_`.

A key **acts as you**, never more: it can only hold permissions your role has, and it is bound by your weekly
limits and approval rules. Revoke it any time on the same screen and it stops working at once.

| Tick | What it lets a key do | Used by Claude's tools? |
|---|---|---|
| **See** | Look at titles, the library, the calendar, your asks and health | yes |
| **Ask** | Ask for titles as you, under your limits | yes |
| **Control downloads** | Pause, resume, remove downloads; grab releases; the `downloads` tool | read-only `downloads` tool only |
| **Change settings** | Change settings, apps and passwords through the REST API | **never** |
| **Delete** | Delete files and titles through the REST API | **never** |
| **Approve** | Approve and decline asks through the REST API | **never** |
| **Admin** | Manage people and limits through the REST API | **never** |

The last four exist for scripts that use the [REST API](api.md) with the same key. Tick them only for such a
script, never for a key you give Claude. Keys cannot create other keys; that is only possible when signed in
from the browser.

## Connect Claude Code

Run this in a terminal on the machine where you use Claude Code:

```sh
claude mcp add --transport http marquee https://<host>/mcp --header "Authorization: Bearer <key>"
```

Replace `<host>` with the address you reach Marquee at, and `<key>` with the key from above. On a home
network, `http://192.168.1.10:6464/mcp` works as well. To reach Marquee from outside your home, put it behind
HTTPS first ([reverse proxy](reverse-proxy.md)): a key sent over plain HTTP across the internet can be read by
anyone on the way. Add `--scope user` to make it available in every project.

Check it with `claude mcp list`, then ask Claude something like "what's downloading on Marquee?".

The endpoint is `POST /mcp` (also reachable at `/api/v1/mcp`). It speaks JSON-RPC over Streamable HTTP, the
2025 protocol revisions from `2024-11-05` to `2025-11-25` and the `2026-07-28` one, and answers every call
with plain JSON. Sign-in is by Bearer key only; there is no OAuth.

## The tools

| Tool | Needs | What it does |
|---|---|---|
| `search_titles` | See | Search for a movie, show or artist. Shows what Marquee already tracks and looks it up at TMDB, TVmaze and MusicBrainz. |
| `library_lookup` | See | For one title: the files on disk (movie), each season and which episodes are on disk, missing or still to air (show), or the albums (artist). |
| `calendar` | See | Episodes of monitored shows airing in the next 1 to 30 days (default 7). |
| `downloads` | Control downloads, or an owner's or admin's key with See | What is downloading now: progress, speed and time left. Read-only. |
| `my_asks` | See | Your asks, newest first, with where each stands. |
| `health` | See | Whether Marquee is running well. For owner and admin keys it also lists your download apps, sources and media servers and whether each answers. |
| `ask_for_title` | Ask | Ask for a title in HD or 4K. Takes two calls, see below. |

Members' keys never get the `downloads` tool, whatever is ticked.

### Asking is a two-step confirm

1. Claude calls `ask_for_title` and gets back a **plan**: what would be asked, the quality, whether the owner will
   have to say yes, and this week's usage, plus a `confirmToken` valid for 5 minutes.
2. Claude shows you the plan. **Only if you agree** does it call again, with the token, and the ask is placed.

The ask then follows the normal [rules](requests.md): your limits, and a member's 4K ask waits for the owner's
yes. An ask that needs approval shows up in the owner's Requests list.

## Safety

- **No approve, decline, delete or settings tool exists.** Not for keys with those ticks either.
- A key can never do more than the person it belongs to, and can be limited to **See** only.
- **Limits:** 60 calls a minute and 5 confirmed asks an hour, per key. They are counted in memory and start
  from zero after a restart.
- Descriptions of films and shows come from outside databases. Marquee tells Claude to treat them as data, not
  as instructions.
- Marquee logs one line per call (the tool, the person, the key id, whether it worked and how long it took),
  **never what was asked**. **Settings → Claude & apps → Recent calls** shows the last 200; the owner sees
  everyone's.
- API keys are stored hashed, and the secret cannot be read back. If one leaks, revoke it.
- Over the internet, always use HTTPS, and think twice before ticking anything beyond See and Ask.
