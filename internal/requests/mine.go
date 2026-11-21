package requests

import "context"

// MyLimits is the limits and flags that apply to one person right now, and what
// they have already used over the last 7 days.
type MyLimits struct {
	Effective EffectiveLimits `json:"effective"`
	Usage     Usage           `json:"usage"`
}

// MyLimits works out, for the person an Actor stands for, which limits apply,
// whether their asks are approved without waiting, and this week's usage. It
// only reads. The MCP server uses it to show what an ask would do before the ask
// is made.
func (s *Service) MyLimits(ctx context.Context, actor Actor) (MyLimits, error) {
	if actor.UserID <= 0 || !validRole(actor.Role) {
		return MyLimits{}, ErrForbidden
	}
	eff, _, err := s.limitsFor(ctx, actor.UserID, actor.Role, s.Config(ctx))
	if err != nil {
		return MyLimits{}, err
	}
	use, err := s.usage(ctx, actor.UserID, eff)
	if err != nil {
		return MyLimits{}, err
	}
	return MyLimits{Effective: eff, Usage: use}, nil
}

func validRole(role string) bool { return role == "owner" || role == "admin" || role == "member" }
