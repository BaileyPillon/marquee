package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// ask_for_title is the only tool that changes anything, and it can only add an
// ask. It cannot approve one: whether an ask waits for the owner is decided by
// the rules in the requests module, exactly as for the person typing it into the
// web UI, under that person's own limits.
//
// It takes two calls. The first returns a plan and a token; the second, with the
// token, places the ask. The token is random, lives 5 minutes, works once, and is
// bound to the key and to every input of the ask.

const maxNoteRunes = 500

// askKey is every input of an ask in its plain form. A confirm token is bound to
// its hash.
type askKey struct {
	TitleID    int64  `json:"titleId"`
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	ProviderID string `json:"providerId"`
	Quality    string `json:"quality"`
	Seasons    []int  `json:"seasons"`
	Note       string `json:"note"`
}

type askTitleOut struct {
	TitleID    int64  `json:"titleId,omitempty"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Year       int    `json:"year,omitempty"`
	InCatalog  bool   `json:"inCatalog"`
	Provider   string `json:"provider,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
}

type existingAskOut struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
	Yours bool   `json:"yours"`
}

type askPlanOut struct {
	Title              askTitleOut     `json:"title"`
	Quality            string          `json:"quality"`
	Scope              string          `json:"scope,omitempty"`
	NeedsOwnerApproval bool            `json:"needsOwnerApproval"`
	Usage              usagePairOut    `json:"usage"`
	HourlyAsksLeft     int             `json:"hourlyAsksLeft"`
	ExistingAsk        *existingAskOut `json:"existingAsk,omitempty"`
}

type askOut struct {
	Step         string        `json:"step"`
	Message      string        `json:"message"`
	CanAsk       *bool         `json:"canAsk,omitempty"`
	ConfirmToken string        `json:"confirmToken,omitempty"`
	ExpiresAt    string        `json:"expiresAt,omitempty"`
	Plan         *askPlanOut   `json:"plan,omitempty"`
	Request      *askItemOut   `json:"request,omitempty"`
	AutoApproved *bool         `json:"autoApproved,omitempty"`
	Usage        *usagePairOut `json:"usage,omitempty"`
}

// normQuality accepts hd and uhd, and the obvious other ways to write them.
func normQuality(q string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "hd", "1080p", "720p":
		return requests.QualityHD, true
	case "uhd", "4k", "2160p":
		return requests.QualityUHD, true
	}
	return "", false
}

// parseAsk reads and checks the arguments of ask_for_title.
func parseAsk(raw json.RawMessage) (k askKey, token string, err error) {
	var a struct {
		TitleID      flexInt   `json:"titleId"`
		Kind         string    `json:"kind"`
		Provider     string    `json:"provider"`
		ProviderID   flexStr   `json:"providerId"`
		Quality      string    `json:"quality"`
		Seasons      []flexInt `json:"seasons"`
		Note         string    `json:"note"`
		ConfirmToken string    `json:"confirmToken"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return k, "", err
	}
	q, ok := normQuality(a.Quality)
	if !ok {
		return k, "", userErrorf(`"quality" must be hd or uhd (4K).`)
	}
	k.Quality = q

	switch {
	case a.TitleID < 0:
		return k, "", userErrorf(`"titleId" must be the id of a title (from search_titles).`)
	case a.TitleID > 0:
		k.TitleID = int64(a.TitleID)
	default:
		k.Kind = strings.ToLower(strings.TrimSpace(a.Kind))
		if !metadata.ValidKind(k.Kind) {
			return k, "", userErrorf(`Name the title: give "titleId" (from search_titles), or "kind" (movie, show or artist) with "providerId".`)
		}
		k.ProviderID = strings.TrimSpace(string(a.ProviderID))
		if k.ProviderID == "" {
			return k, "", userErrorf(`Name the title: give "titleId" (from search_titles), or "kind" with "providerId".`)
		}
		want := metadata.ProviderFor(k.Kind)
		k.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
		if k.Provider == "" {
			k.Provider = want
		}
		if k.Provider != want {
			return k, "", userErrorf("A %s comes from %s, not %q.", k.Kind, want, k.Provider)
		}
		if k.Kind == metadata.KindArtist {
			k.ProviderID = strings.ToLower(k.ProviderID)
			if !metadata.IsMBID(k.ProviderID) {
				return k, "", userErrorf("An artist's providerId is a MusicBrainz id (a UUID).")
			}
		} else if n, perr := strconv.ParseInt(k.ProviderID, 10, 64); perr != nil || n <= 0 {
			return k, "", userErrorf("A %s's providerId is a whole number (its %s id).", k.Kind, want)
		}
	}

	for _, n := range a.Seasons {
		if n < 0 || n > 9999 {
			return k, "", userErrorf(`"seasons" holds season numbers (0 is specials).`)
		}
		if !slices.Contains(k.Seasons, int(n)) {
			k.Seasons = append(k.Seasons, int(n))
		}
	}
	slices.Sort(k.Seasons)

	k.Note = strings.TrimSpace(a.Note)
	if utf8.RuneCountInString(k.Note) > maxNoteRunes {
		return k, "", userErrorf(`"note" is too long (at most %d characters).`, maxNoteRunes)
	}
	return k, strings.TrimSpace(a.ConfirmToken), nil
}

func (s *Server) askForTitle(ctx context.Context, c *toolCall) (*toolOutput, error) {
	key, token, err := parseAsk(c.Args)
	if err != nil {
		return nil, err
	}
	title, err := s.resolveAskTitle(ctx, key)
	if err != nil {
		return nil, err
	}
	switch {
	case title.Kind != metadata.KindShow && len(key.Seasons) > 0:
		return nil, userErrorf(`"seasons" only applies to a show.`)
	case title.Kind == metadata.KindArtist && key.Quality == requests.QualityUHD:
		return nil, userErrorf(`Music comes in one quality. Ask for "hd".`)
	}
	if token == "" {
		return s.askPlan(ctx, c, key, title)
	}
	return s.askConfirm(ctx, c, key, token, title)
}

// resolveAskTitle names the title an ask is for. It reads only: a title that is
// not in the catalog is looked up at its provider, and is not saved until the
// ask is placed.
func (s *Server) resolveAskTitle(ctx context.Context, k askKey) (askTitleOut, error) {
	id := k.TitleID
	if id == 0 {
		found, ok, err := s.store.FindTitle(ctx, k.Kind, k.ProviderID)
		if err != nil {
			return askTitleOut{}, err
		}
		if ok {
			id = found
		}
	}
	if id > 0 {
		d, err := s.d.Titles.Get(ctx, id)
		if errors.Is(err, titles.ErrNotFound) {
			return askTitleOut{}, userErrorf("Marquee does not track a title with id %d. Find the right one with search_titles.", id)
		}
		if err != nil {
			return askTitleOut{}, err
		}
		provider, pid := providerIDOf(d.Title)
		return askTitleOut{TitleID: d.ID, Kind: d.Kind, Name: d.Name, Year: d.Year, InCatalog: true, Provider: provider, ProviderID: pid}, nil
	}

	out := askTitleOut{Kind: k.Kind, Provider: k.Provider, ProviderID: k.ProviderID}
	var err error
	switch k.Kind {
	case metadata.KindMovie:
		if s.d.Providers.Movies == nil {
			return out, userErrorf("Movies cannot be looked up on this Marquee.")
		}
		var info *metadata.MovieInfo
		if info, err = s.d.Providers.Movies.Movie(ctx, k.ProviderID); err == nil {
			out.Name, out.Year = info.Title, info.Year
		}
	case metadata.KindShow:
		if s.d.Providers.Shows == nil {
			return out, userErrorf("Shows cannot be looked up on this Marquee.")
		}
		var info *metadata.ShowInfo
		if info, err = s.d.Providers.Shows.Show(ctx, k.ProviderID); err == nil {
			out.Name, out.Year = info.Name, info.Year
		}
	default:
		if s.d.Providers.Artists == nil {
			return out, userErrorf("Artists cannot be looked up on this Marquee.")
		}
		var info *metadata.ArtistInfo
		if info, err = s.d.Providers.Artists.Artist(ctx, k.ProviderID); err == nil {
			out.Name, out.Year = info.Name, info.Year
		}
	}
	if err != nil {
		if errors.Is(err, metadata.ErrNotFound) {
			return out, userErrorf("%s has no %s with id %s. Check it with search_titles.", k.Provider, k.Kind, k.ProviderID)
		}
		if metadata.CodeOf(err) != "" {
			return out, userErrorf("%s", metadata.MessageOf(err))
		}
		return out, err
	}
	return out, nil
}

func titleLine(t askTitleOut) string {
	return fmt.Sprintf("%q%s (%s)", t.Name, yearSuffix(t.Year), t.Kind)
}

func scopeLine(k askKey, t askTitleOut) string {
	switch {
	case t.Kind != metadata.KindShow:
		return ""
	case len(k.Seasons) == 0:
		return "all seasons"
	}
	return scopeText(requests.Scope{Seasons: k.Seasons})
}

func (s *Server) askPlan(ctx context.Context, c *toolCall, key askKey, title askTitleOut) (*toolOutput, error) {
	now := s.now()
	lim, err := s.d.Requests.MyLimits(ctx, actorOf(c.P))
	if err != nil {
		return nil, err
	}
	usage := usageFrom(lim.Usage)
	approvedAtOnce := lim.Effective.AutoApproveHD
	if key.Quality == requests.QualityUHD {
		approvedAtOnce = lim.Effective.AutoApproveUHD
	}
	left, resets := s.asks.left(c.P.KeyID, now)

	plan := askPlanOut{
		Title: title, Quality: key.Quality, Scope: scopeLine(key, title),
		NeedsOwnerApproval: !approvedAtOnce, Usage: usage, HourlyAsksLeft: left,
	}
	var open *OpenAsk
	if title.InCatalog {
		if open, err = s.store.OpenAsk(ctx, title.TitleID, key.Quality); err != nil {
			return nil, err
		}
	}
	if open != nil {
		plan.ExistingAsk = &existingAskOut{ID: open.ID, State: open.State, Yours: open.UserID == c.P.User.ID}
	}

	// Why the ask cannot be placed right now, if it cannot.
	var blocked string
	u := usage.of(key.Quality)
	switch {
	case open != nil && open.State == requests.StateAvailable:
		blocked = fmt.Sprintf("%s is already available in %s.", titleLine(title), qualityWord(key.Quality))
	case open != nil && plan.ExistingAsk.Yours:
		blocked = fmt.Sprintf("You already asked for %s in %s (ask #%d, %s).", titleLine(title), qualityWord(key.Quality), open.ID, stateWords(open.State))
	case open != nil:
		blocked = fmt.Sprintf("Someone has already asked for %s in %s (%s). Nothing more is needed.", titleLine(title), qualityWord(key.Quality), stateWords(open.State))
	case u.Limit != nil && u.Used >= *u.Limit:
		blocked = fmt.Sprintf("You have used all %d of your %s asks for this week.", *u.Limit, qualityWord(key.Quality))
		if u.ResetsAt != "" {
			blocked += " A place frees up at " + u.ResetsAt + "."
		}
	case left == 0:
		blocked = fmt.Sprintf("This key has placed %d asks in the last hour, which is the most it may place. It can ask again at %s.",
			s.d.AsksPerHour, resets.Local().Format("15:04"))
	}

	out := askOut{Step: "plan", Plan: &plan}
	canAsk := blocked == ""
	out.CanAsk = &canAsk

	var b strings.Builder
	if !canAsk {
		b.WriteString(blocked + " No ask was placed, and no confirmToken was issued.")
		out.Message = b.String()
		return &toolOutput{Text: out.Message, Data: out}, nil
	}

	token, exp, err := s.tokens.issue(c.P.KeyID, c.P.User.ID, inputHash(key), now)
	if err != nil {
		return nil, err
	}
	out.ConfirmToken, out.ExpiresAt = token, exp.UTC().Format(time.RFC3339)

	fmt.Fprintf(&b, "Plan: ask for %s in %s", titleLine(title), qualityWord(key.Quality))
	if plan.Scope != "" {
		b.WriteString(", " + plan.Scope)
	}
	b.WriteString(".")
	if !title.InCatalog {
		b.WriteString(" Marquee does not track it yet and will add it to the catalog.")
	}
	if plan.NeedsOwnerApproval {
		b.WriteString(" It will wait for the owner's yes before Marquee starts looking for it.")
	} else {
		b.WriteString(" It is approved straight away, and Marquee starts looking for it at once.")
	}
	if u.Limit != nil {
		fmt.Fprintf(&b, " This week you have used %d of %d %s asks.", u.Used, *u.Limit, qualityWord(key.Quality))
	} else {
		fmt.Fprintf(&b, " This week you have made %d %s asks (no limit).", u.Used, qualityWord(key.Quality))
	}
	fmt.Fprintf(&b, "\nNothing has been asked yet. Show this to the person and, only if they agree, call ask_for_title again with the same arguments and confirmToken %q. "+
		"The token works once and expires at %s.", token, exp.Local().Format("15:04"))
	out.Message = b.String()
	return &toolOutput{Text: out.Message, Data: out}, nil
}

func tokenMessage(err error) string {
	const again = " Call ask_for_title again without a confirmToken to get a new plan."
	switch {
	case errors.Is(err, errTokenUsed):
		return "That confirmToken was already used, so no second ask was placed." + again
	case errors.Is(err, errTokenMismatch):
		return "That confirmToken was issued for a different ask: every argument must be the same as in the plan." + again
	}
	return "That confirmToken is not valid, or it has expired (it lasts 5 minutes)." + again
}

func (s *Server) askConfirm(ctx context.Context, c *toolCall, key askKey, token string, title askTitleOut) (*toolOutput, error) {
	now := s.now()
	keyID := c.P.KeyID
	hash := inputHash(key)
	if err := s.tokens.check(token, keyID, hash, now); err != nil {
		return nil, &userError{msg: tokenMessage(err)}
	}
	// A place in this hour's allowance, taken before anything is changed.
	if ok, _ := s.asks.take(keyID, now); !ok {
		_, resets := s.asks.left(keyID, now)
		return nil, userErrorf("This key has placed %d asks in the last hour, which is the most it may place. It can ask again at %s.",
			s.d.AsksPerHour, resets.Local().Format("15:04"))
	}
	if err := s.tokens.redeem(token, keyID, hash, now); err != nil {
		s.asks.give(keyID, now)
		return nil, &userError{msg: tokenMessage(err)}
	}

	in := requests.AskInput{
		Quality: key.Quality, Note: key.Note, SourceRef: "mcp-key-" + strconv.FormatInt(keyID, 10),
		Scope: requests.Scope{Seasons: key.Seasons},
	}
	if title.InCatalog {
		in.TitleID = title.TitleID
	} else {
		in.Kind, in.Provider, in.ProviderID = key.Kind, key.Provider, key.ProviderID
	}
	res, err := s.d.Requests.Ask(ctx, actorOf(c.P), in)
	if err != nil {
		s.asks.give(keyID, now) // nothing was asked: the place goes back
		return nil, s.askError(err, key)
	}

	item := askItemFrom(res.Request)
	usage := usageFrom(res.Usage)
	auto := res.AutoApproved
	var b strings.Builder
	fmt.Fprintf(&b, "Asked for %s in %s (ask #%d).", titleLine(title), qualityWord(key.Quality), item.ID)
	if res.AutoApproved {
		b.WriteString(" It was approved straight away and Marquee is looking for it now.")
	} else {
		b.WriteString(" It is waiting for the owner's yes, which only they can give, inside Marquee.")
	}
	out := askOut{Step: "done", Message: b.String(), Request: &item, AutoApproved: &auto, Usage: &usage}
	return &toolOutput{Text: out.Message, Data: out}, nil
}

// askError turns what the requests module refuses with into a message for Claude.
// Anything it does not know comes back as it is, and the caller logs it.
func (s *Server) askError(err error, key askKey) error {
	var (
		inv *requests.InvalidError
		lim *requests.LimitError
		dup *requests.DuplicateError
	)
	switch {
	case errors.As(err, &inv):
		return userErrorf("%s", inv.Message)
	case errors.As(err, &lim):
		msg := fmt.Sprintf("You have used all %d of your %s asks for this week.", lim.Limit, qualityWord(lim.Quality))
		if lim.ResetsAt != nil {
			msg += " A place frees up at " + lim.ResetsAt.UTC().Format(time.RFC3339) + "."
		}
		return &userError{msg: msg}
	case errors.As(err, &dup):
		return userErrorf("That title is already asked for in %s (ask #%d, %s). Nothing more was needed.",
			qualityWord(key.Quality), dup.Existing.ID, stateWords(dup.Existing.State))
	case errors.Is(err, requests.ErrNoFolder):
		return userErrorf("Marquee has no library folder for this kind of title yet. The owner needs to add one in Settings, then the ask can be placed.")
	case errors.Is(err, requests.ErrForbidden):
		return userErrorf("This person is not allowed to ask.")
	case errors.Is(err, titles.ErrNotFound):
		return userErrorf("That title no longer exists in Marquee. Search for it again.")
	case metadata.CodeOf(err) != "":
		return userErrorf("%s", metadata.MessageOf(err))
	}
	return err
}
