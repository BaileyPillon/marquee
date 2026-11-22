# fakefeed

A tiny Torznab server for local end-to-end tests and for people working on Marquee. It serves the releases
you list in a JSON file, in the XML a real Torznab indexer (Jackett, Prowlarr) answers with. Go standard
library only, one file, and it is **not** part of Marquee: nothing in the program imports it.

It serves addresses and numbers you wrote down and nothing else. It does not fetch, host or download any
torrent. (Marquee's own download app does that when you ask for a title and a release is picked.)

## Run it

From the repository root:

```powershell
go run ./tools/fakefeed -items tools/fakefeed/example-items.json
```

```
time=... level=INFO msg="fakefeed listening" url=http://127.0.0.1:9117/api items=4 apikey_required=false
```

Stop it with Ctrl+C. The items file is read once at start, so restart it after editing the file.

| Flag | Default | |
|---|---|---|
| `-addr` | `127.0.0.1:9117` | listen address. Use `0.0.0.0:9117` for a Marquee running in Docker (then add the source as `http://host.docker.internal:9117/api`) |
| `-items` | none | JSON file with the releases, a list. Without it the feed is empty (the caps still answer) |
| `-apikey` | none | if set, every request to `/api` needs `?apikey=<this>`, else it gets Torznab error 100 |

## Add it to Marquee

In Settings, add a source of kind **Torznab** with the address `http://127.0.0.1:9117/api` and the key if you
set one, then press Test. It should say it searches for movies and can search by IMDb and TMDB id.

## What it answers

Everything is at `/api`. Anything else is a 404, a non-GET is a 405.

| Request | Answer |
|---|---|
| `?t=caps` | the capabilities: modes `search`, `movie-search`, `tv-search`, `music-search`; supported parameters `q`, `imdbid`, `tmdbid`, `season`, `ep`; categories 2000 Movies (2040 HD, 2045 UHD), 3000 Audio, 5000 TV; limits max 1000, default 100 |
| `?t=search`, `movie`, `tvsearch` or `music` | an RSS 2.0 feed with the `torznab` namespace, filtered as below |
| no `t` | Torznab error 200 |
| any other `t` | Torznab error 202 |
| a wrong or missing `apikey` (only when `-apikey` is set; caps too) | Torznab error 100 (`<error code="100" .../>`, in an HTTP 200 answer, as Jackett does) |

**Filtering** (all four search modes behave the same):

- `q`: a case-insensitive substring of the title. Dots, dashes, underscores and spaces count alike, so
  Marquee's `Sintel 2010` finds `Sintel.2010.720p.WEB-DL.x264-Blender`.
- `imdbid` and `tmdbid`: exact match against the item's ids. `imdbid` can be `tt1727587` or `1727587`
  (Marquee sends the digits). If both are given, either one matching is enough. An item with no id never
  matches an id search.
- If `q` is given together with an id, both must fit.
- With none of `q`, `imdbid`, `tmdbid` every item is returned: that is the **RSS mode**, which Marquee polls.
- `limit` (default 100, at most 1000) and `offset` are honoured. `torznab:response total` is the number that
  matched.
- `season` and `ep` are listed in the caps so Marquee sends them as their own parameters, but they are
  **not** used to filter: the answer is every release that fits `q` or the ids. `cat`, `extended` and any
  other parameter are accepted and ignored.

Each `<item>` has `title`, `guid`, `link`, `enclosure` (`url`, `length`, `type="application/x-bittorrent"`),
`pubDate`, `size`, and these `torznab:attr`: `size`, `seeders`, `peers`, `infohash` and `magneturl` (when the
item has them), `category` (the parent too, so 2040 also lists 2000), `imdb` (digits only), `tmdbid`,
`downloadvolumefactor` = 0 and `uploadvolumefactor` = 1. A magnet-only item uses its magnet as the link.

Every request is logged on one line. Values of `apikey`, `api_key`, `key`, `passkey`, `rsskey`, `authkey`,
`torrent_pass` and `token` are replaced by `REDACTED`, so a log can be pasted into a bug report.

```
time=... level=INFO msg=request remote=127.0.0.1:50321 method=GET path=/api query="apikey=REDACTED&q=sintel&t=search" result="1 items"
```

## The items file

A JSON list. Only `title` and one of `link` or `magnet` are required; a mistake stops the start with a
message naming the item (an unknown field is a mistake, so a typo does not go unnoticed).

| Field | Type | |
|---|---|---|
| `title` | string | the release name |
| `link` | string | address of the `.torrent` file |
| `magnet` | string | a `magnet:` link; used as the link when `link` is empty |
| `size` | number | bytes |
| `seeders`, `peers` | number | |
| `imdbid` | string | `"tt1727587"` or the digits; may be empty |
| `tmdbid` | string or number | `"45745"` or `45745`; may be empty |
| `category` | number | for example 2040 (Movies/HD), 2045 (Movies/UHD), 5000 (TV), 3000 (Audio) |
| `infohash` | string | 40 hex characters, or empty (Marquee reads it from the magnet if it can) |
| `pubDate` | string | `Sat, 21 Nov 2026 12:00:00 +0000` (RFC 1123, RFC 822 and RFC 3339 also work). Empty means the time of the request |

## The example file

`example-items.json` lists only Blender Foundation's open movies, which are free to share under Creative
Commons licences (this matches the test-library rule in `SPEC.md`):

| Film | Licence |
|---|---|
| Sintel (2010) | CC-BY 3.0, Blender Foundation |
| Big Buck Bunny (2008) | CC-BY 3.0, Blender Foundation |
| Tears of Steel (2012) | CC-BY 3.0, Blender Foundation |
| Cosmos Laundromat (2015) | CC-BY 4.0, Blender Institute |

The links follow the `https://webtorrent.io/torrents/<name>.torrent` addresses WebTorrent publishes for these
films. The sizes, seeder and peer counts and dates are **made-up test values**, not the real torrents' numbers.
Only Sintel has ids; the other entries leave them empty on purpose (add the real ones if you need an id
search for them). The links were not fetched or checked when this file was written.

To use your own films, copy the file and edit it. Keep to public-domain or Creative Commons titles for anything
that is committed to the repository.

## Tests

```powershell
go test ./tools/fakefeed
```

The tests start the server and read its answers with Marquee's own `internal/sources` parser (`ParseCaps`,
`ParseFeed`, `BuildSearch`) to prove the two stay compatible. Importing an internal package from `tools/` is
fine inside the same module; the `fakefeed` program itself imports nothing outside the standard library.
