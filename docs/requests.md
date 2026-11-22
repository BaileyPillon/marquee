# Requests

Requests are how friends and family get things onto your server without you doing the work. A person **asks**
for a movie, a show or an artist; Marquee checks the ask against your rules; if it is fine, Marquee finds a
release, downloads it, imports it and tells your media server. You only get involved when a rule says so.

## People

The first person to open Marquee is the **owner**. Everyone else joins by **invite**:

- The owner (or an admin) makes a one-time invite link for a role. It works for 7 days, once.
- **Member**: can search, ask, and see their own asks. Members only ever see their own asks.
- **Admin**: everything a member can do, plus approving asks, searching and grabbing releases, downloads and the
  people list. An admin's own asks go straight through.
- **Owner**: everything, including every setting, API keys with all permissions and deleting titles.

## An ask

An ask names a title and a **quality**, and may carry a short note for the owner:

- **Movies and shows**: HD (1080p) or 4K (2160p). For a show you can ask for all episodes or chosen seasons.
- **Music**: one quality only. An ask for an artist covers all their recordings, or chosen albums.
- **One open ask per title and quality.** Asking again for something already asked for is refused, with a
  pointer to the existing ask.

An ask is in one of these states: **waiting for you** (pending), **approved**, **declined**, **available**
(it is in the library), **partial** (some of it is), **failed**, or **cancelled**. A person can cancel their own ask
while it is waiting; the owner can cancel any that is not finished.

## 4K is a separate copy

A 4K ask does not replace the HD file. It creates a **second copy** of the title that lives in its own library
folder, next to the HD one, with its own quality rules, so a 4K file never overwrites or hides the 1080p one, and a
media server can keep them apart.

Marquee chooses the folder for a 4K copy like this:

1. The folder the owner picked for that kind of title and quality, if there is one.
2. Otherwise the first folder of the right kind that has `4k` or `uhd` in its name, label or path.
3. Otherwise the folder the HD copy uses.

Plain HD asks never use a folder that looks like a 4K one if another is available. The sensible setup is two
folders per kind: `/data/movies` and `/data/movies-4k` (see [folders](connections.md#folders-and-hardlinks)).
Each copy has its own [quality profile](configuration.md#what-you-set-in-the-web-page): a 4K ask follows the
4K rules (for example a size limit), so Marquee will not grab a 70 GB remux unless you allow it.

## Approvals

Who needs a yes, by default:

| Who asks | HD | 4K |
|---|---|---|
| Owner or admin | goes straight through | goes straight through |
| Member | goes straight through (default) | **waits for the owner or an admin** (default) |

Each of those is a default you can change: **Settings → People & limits** has the defaults for everyone, and
**Edit limits** on a person overrides them for just that person. The owner and admins' own asks are always
approved at once.

An ask that waits shows up in **Requests** (and on the owner's phone) with the reason it is waiting. The owner can:

- **Approve.** The title becomes monitored and Marquee starts looking for a release.
- **Approve and always allow this person 4K.** The same, and from now on that person's 4K asks go through
  on their own.
- **Decline**, with a reason the person can see.

If there is no library folder for that kind of title, Marquee cannot approve anything for it: a member's ask waits,
and the owner is told to add the folder in Settings first.

## Limits

Every member has a **weekly limit** for HD asks and one for 4K asks. By default it is 10 HD and 2 4K.

- The week is a **rolling 7 days**, counted from each ask, not a calendar week. When the limit is used up, the
  next ask is refused with a message saying when a place frees up.
- Asks that were declined or cancelled do not count.
- `0` means "no asks at all" for that quality. Clear the box on a person to give them the default again.
- The owner and admins have no limit unless you set one for them by name.

The limits apply to every way of asking: the web page, Claude, and watchlists.

## Watchlists

If you link your Plex account ([connections](connections.md#plex-watchlists)), Marquee checks every 15 minutes
and turns watchlist items into asks:

- Your own items are asks from you.
- A Plex friend's items are asks from the Marquee person you linked them to. A friend with no linked person is
  skipped until you link one, or you can let Marquee make a member for each new friend. Those items are then
  retried at the next check.
- They ask at the quality you pick for watchlists (HD by default), so the person's limits and the approval
  rules above apply exactly as if they had asked by hand.
- Each item is looked at once. Marquee skips, and tells you why, items it cannot match (for example a movie
  with no TMDB id, or nothing found), and items someone has already asked for.

## Claude

An API key (for Claude Code, for example) acts **as the person it belongs to**, under that person's limits and
approval rules, and only with the permissions ticked on the key. Claude can *ask* but can never approve. See
[Claude and MCP](claude-mcp.md).
