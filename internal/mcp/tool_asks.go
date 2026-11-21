package mcp

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/requests"
)

const (
	defaultAsksLimit = 20
	maxAsksLimit     = 50
)

type askItemOut struct {
	ID            int64  `json:"id"`
	TitleID       int64  `json:"titleId"`
	Title         string `json:"title"`
	Year          int    `json:"year,omitempty"`
	Kind          string `json:"kind"`
	Quality       string `json:"quality"`
	State         string `json:"state"`
	Scope         string `json:"scope,omitempty"`
	Note          string `json:"note,omitempty"`
	DeclineReason string `json:"declineReason,omitempty"`
	CreatedAt     string `json:"createdAt"`
	DecidedAt     string `json:"decidedAt,omitempty"`
}

func scopeText(sc requests.Scope) string {
	switch {
	case sc.All:
		return "all"
	case len(sc.Seasons) > 0:
		parts := make([]string, len(sc.Seasons))
		for i, n := range sc.Seasons {
			parts[i] = strconv.Itoa(n)
		}
		if len(parts) == 1 {
			return "season " + parts[0]
		}
		return "seasons " + strings.Join(parts, ", ")
	case len(sc.Albums) > 0:
		return plural(len(sc.Albums), "album", "albums")
	}
	return ""
}

func askItemFrom(it requests.Item) askItemOut {
	o := askItemOut{
		ID: it.ID, TitleID: it.Title.ID, Title: it.Title.Name, Year: it.Title.Year, Kind: it.Title.Kind,
		Quality: it.Quality, State: it.State, Scope: scopeText(it.Scope), Note: it.Note,
		DeclineReason: it.DeclineReason, CreatedAt: it.CreatedAt.UTC().Format(time.RFC3339),
	}
	if it.DecidedAt != nil {
		o.DecidedAt = it.DecidedAt.UTC().Format(time.RFC3339)
	}
	return o
}

// stateWords says where an ask stands, for people.
func stateWords(state string) string {
	switch state {
	case requests.StatePending:
		return "waiting for the owner's yes"
	case requests.StateApproved:
		return "approved, Marquee is looking for it"
	case requests.StateAvailable:
		return "available in the library"
	case requests.StatePartial:
		return "partly available"
	case requests.StateFailed:
		return "failed (the owner can try again)"
	case requests.StateDeclined:
		return "declined by the owner"
	case requests.StateCancelled:
		return "cancelled"
	}
	return state
}

func actorOf(p *accounts.Principal) requests.Actor {
	return requests.Actor{UserID: p.User.ID, Role: string(p.User.Role), Source: requests.SourceMCP}
}

type myAsksOut struct {
	Total int          `json:"total"`
	Asks  []askItemOut `json:"asks"`
}

func (s *Server) myAsks(ctx context.Context, c *toolCall) (*toolOutput, error) {
	var a struct {
		State string  `json:"state"`
		Limit flexInt `json:"limit"`
	}
	if err := decodeArgs(c.Args, &a); err != nil {
		return nil, err
	}
	state := strings.ToLower(strings.TrimSpace(a.State))
	if state != "" && !slices.Contains(requests.States, state) {
		return nil, userErrorf(`"state" must be one of %s.`, strings.Join(requests.States, ", "))
	}
	limit := defaultAsksLimit
	if a.Limit != 0 {
		if a.Limit < 1 || a.Limit > maxAsksLimit {
			return nil, userErrorf(`"limit" must be a whole number from 1 to %d.`, maxAsksLimit)
		}
		limit = int(a.Limit)
	}
	res, err := s.d.Requests.List(ctx, actorOf(c.P), requests.ListFilter{State: state, Mine: true, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := myAsksOut{Total: res.Total, Asks: make([]askItemOut, 0, len(res.Items))}
	for _, it := range res.Items {
		out.Asks = append(out.Asks, askItemFrom(it))
	}

	var b strings.Builder
	switch {
	case res.Total == 0 && state == "":
		b.WriteString("You have not asked for anything yet.")
	case res.Total == 0:
		fmt.Fprintf(&b, "You have no %s asks.", state)
	default:
		fmt.Fprintf(&b, "%s", plural(res.Total, "ask", "asks"))
		if state != "" {
			fmt.Fprintf(&b, " in state %s", state)
		}
		if res.Total > len(out.Asks) {
			fmt.Fprintf(&b, " (showing the newest %d)", len(out.Asks))
		}
		b.WriteString(":")
		for _, it := range out.Asks {
			fmt.Fprintf(&b, "\n- #%d %s%s, %s, %s: %s", it.ID, it.Title, yearSuffix(it.Year), kindWord(it.Kind), qualityWord(it.Quality), stateWords(it.State))
			if it.Scope != "" && it.Scope != "all" {
				b.WriteString(" (" + it.Scope + ")")
			}
			if it.DeclineReason != "" {
				b.WriteString(". Reason: " + shorten(it.DeclineReason, 200))
			}
		}
	}
	return &toolOutput{Text: b.String(), Data: out}, nil
}

// ---- the plan and the answer of ask_for_title --------------------------------------

type usageOut struct {
	Used     int    `json:"used"`
	Limit    *int   `json:"limit,omitempty"`
	ResetsAt string `json:"resetsAt,omitempty"`
}

type usagePairOut struct {
	HD  usageOut `json:"hd"`
	UHD usageOut `json:"uhd"`
}

func usageFrom(u requests.Usage) usagePairOut {
	conv := func(q requests.QualityUsage) usageOut {
		o := usageOut{Used: q.Used, Limit: q.Limit}
		if q.ResetsAt != nil {
			o.ResetsAt = q.ResetsAt.UTC().Format(time.RFC3339)
		}
		return o
	}
	return usagePairOut{HD: conv(u.HD), UHD: conv(u.UHD)}
}

func (u usagePairOut) of(quality string) usageOut {
	if quality == requests.QualityUHD {
		return u.UHD
	}
	return u.HD
}
