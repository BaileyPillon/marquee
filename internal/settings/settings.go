// Package settings stores the owner's configuration as one JSON object per
// section ("general", "tmdb", ...). Any field whose name ends in Key, Token or
// Password is sealed with the master key before it is written, and is shown
// masked (for example "••••1234") when read back for display.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/secrets"
)

// MaskPrefix starts every masked value. A client that sends a masked value
// back unchanged keeps the stored secret.
const MaskPrefix = "••••"

// Errors.
var (
	ErrBadSection = errors.New("settings: section name must be lowercase letters, digits and dashes")
	ErrNotObject  = errors.New("settings: a section must be a JSON object")
	ErrBadSecret  = errors.New("settings: a secret field must be a string")
)

var sectionName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Service reads and writes settings.
type Service struct {
	store *Store
	box   *secrets.Box
	now   func() time.Time
}

// New builds the service.
func New(d *sql.DB, box *secrets.Box) *Service {
	return &Service{store: NewStore(d), box: box, now: time.Now}
}

// Section is a settings section as shown to the owner: secrets masked.
type Section struct {
	Name      string         `json:"section"`
	Value     map[string]any `json:"value"`
	UpdatedAt *time.Time     `json:"updatedAt"`
}

// IsSecretKey reports whether a field name marks a secret.
func IsSecretKey(k string) bool {
	for _, suffix := range []string{"Key", "Token", "Password"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	switch k {
	case "key", "token", "password":
		return true
	}
	return false
}

// Mask shows only the end of a secret, and only if there is plenty of it.
func Mask(plain string) string {
	if plain == "" {
		return ""
	}
	r := []rune(plain)
	if len(r) >= 12 {
		return MaskPrefix + string(r[len(r)-4:])
	}
	return MaskPrefix
}

func checkSection(name string) error {
	if !sectionName.MatchString(name) {
		return ErrBadSection
	}
	return nil
}

// ---- typed access for other modules ----------------------------------------

// Get decodes a section into dst (a pointer to a struct or map), with secrets
// decrypted. It reports false, leaving dst alone, if the section was never set.
func (s *Service) Get(ctx context.Context, section string, dst any) (bool, error) {
	if err := checkSection(section); err != nil {
		return false, err
	}
	raw, _, found, err := s.store.Get(ctx, section)
	if err != nil || !found {
		return false, err
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false, fmt.Errorf("settings: section %q is not valid JSON: %w", section, err)
	}
	if err := s.walk(v, func(m map[string]any, k, val string) error {
		plain, err := s.box.Open(val)
		if err != nil {
			return fmt.Errorf("settings: cannot decrypt %s.%s (was the master key replaced?): %w", section, k, err)
		}
		m[k] = plain
		return nil
	}); err != nil {
		return false, err
	}
	plain, err := json.Marshal(v)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(plain, dst)
}

// GetAs is Get returning a value of type T.
func GetAs[T any](ctx context.Context, s *Service, section string) (T, bool, error) {
	var v T
	ok, err := s.Get(ctx, section, &v)
	return v, ok, err
}

// Set stores v (a struct or map that encodes as a JSON object) as the whole
// section, sealing secret fields.
func (s *Service) Set(ctx context.Context, section string, v any) error {
	if err := checkSection(section); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return ErrNotObject
	}
	sealed, err := s.seal(obj, nil)
	if err != nil {
		return err
	}
	_, err = s.store.Update(ctx, section, s.now(), func(string, bool) (string, error) { return sealed, nil })
	return err
}

// SetAs is Set with a typed value.
func SetAs[T any](ctx context.Context, s *Service, section string, v T) error {
	return s.Set(ctx, section, v)
}

// ---- display access for the API --------------------------------------------

// GetMasked returns a section for display. A section that was never set comes
// back as an empty object.
func (s *Service) GetMasked(ctx context.Context, section string) (Section, error) {
	if err := checkSection(section); err != nil {
		return Section{}, err
	}
	raw, updated, found, err := s.store.Get(ctx, section)
	if err != nil {
		return Section{}, err
	}
	out := Section{Name: section, Value: map[string]any{}}
	if !found {
		return out, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return Section{}, fmt.Errorf("settings: section %q is not valid JSON: %w", section, err)
	}
	if err := s.walk(obj, func(m map[string]any, k, val string) error {
		if plain, err := s.box.Open(val); err == nil {
			m[k] = Mask(plain)
		} else {
			m[k] = MaskPrefix // cannot read it, but never leak the sealed text
		}
		return nil
	}); err != nil {
		return Section{}, err
	}
	out.Value = obj
	out.UpdatedAt = &updated
	return out, nil
}

// Put replaces a section with what the owner sent. Secret fields are sealed;
// a secret that arrives still masked keeps its stored value; an empty secret
// clears it. Inside arrays, a masked value is matched to the stored element at
// the same position.
func (s *Service) Put(ctx context.Context, section string, incoming map[string]any) (Section, error) {
	if err := checkSection(section); err != nil {
		return Section{}, err
	}
	if incoming == nil {
		return Section{}, ErrNotObject
	}
	_, err := s.store.Update(ctx, section, s.now(), func(old string, found bool) (string, error) {
		var existing map[string]any
		if found {
			if err := json.Unmarshal([]byte(old), &existing); err != nil {
				existing = nil
			}
		}
		sealed, err := s.seal(incoming, existing)
		if err != nil {
			return "", err
		}
		return sealed, nil
	})
	if err != nil {
		return Section{}, err
	}
	return s.GetMasked(ctx, section)
}

// seal turns an incoming object into stored JSON text. existing is the stored
// (already sealed) object at the same position, used to keep masked secrets.
func (s *Service) seal(obj map[string]any, existing map[string]any) (string, error) {
	out, err := s.sealValue(obj, existing)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(out)
	return string(b), err
}

func (s *Service) sealValue(v any, existing any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		old, _ := existing.(map[string]any)
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSecretKey(k) {
				str, isStr := val.(string)
				switch {
				case val == nil:
					out[k] = ""
				case !isStr:
					return nil, fmt.Errorf("%w: %q", ErrBadSecret, k)
				case str == "":
					out[k] = ""
				case strings.HasPrefix(str, MaskPrefix):
					// Unchanged placeholder: keep what is stored (or nothing).
					if prev, ok := old[k].(string); ok {
						out[k] = prev
					} else {
						out[k] = ""
					}
				default:
					sealed, err := s.box.Seal(str)
					if err != nil {
						return nil, err
					}
					out[k] = sealed
				}
				continue
			}
			sub, err := s.sealValue(val, old[k])
			if err != nil {
				return nil, err
			}
			out[k] = sub
		}
		return out, nil
	case []any:
		old, _ := existing.([]any)
		out := make([]any, len(t))
		for i, val := range t {
			var prev any
			if i < len(old) {
				prev = old[i]
			}
			sub, err := s.sealValue(val, prev)
			if err != nil {
				return nil, err
			}
			out[i] = sub
		}
		return out, nil
	default:
		return v, nil
	}
}

// walk calls fn for every non-empty sealed string under a secret-looking key.
func (s *Service) walk(v any, fn func(m map[string]any, key, val string) error) error {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if IsSecretKey(k) {
				if str, ok := val.(string); ok && str != "" {
					if err := fn(t, k, str); err != nil {
						return err
					}
				}
				continue
			}
			if err := s.walk(val, fn); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range t {
			if err := s.walk(val, fn); err != nil {
				return err
			}
		}
	}
	return nil
}
