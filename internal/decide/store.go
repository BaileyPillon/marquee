package decide

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
)

// Store is the only place in this package that talks SQL.
type Store struct {
	db *sql.DB
}

// NewStore wraps the main database.
func NewStore(d *sql.DB) *Store { return &Store{db: d} }

type scanner interface{ Scan(dest ...any) error }

// ---- profiles ----------------------------------------------------------------

const profileCols = `id, name, kind, quality, allowed_res, preferred_res, allowed_sources, min_size_mb, max_size_mb,
	max_size_per_hour_mb, prefer_hdr, preferred_words, rejected_words, min_seeders, upgrade_until, created_at`

func scanProfile(s scanner) (Profile, error) {
	var (
		p                     Profile
		res, srcs, pref, rej  string
		minMB, maxMB, perHour sql.NullInt64
		hdr                   int
		upgrade               sql.NullString
		created               string
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Kind, &p.Quality, &res, &p.PreferredRes, &srcs, &minMB, &maxMB, &perHour,
		&hdr, &pref, &rej, &p.MinSeeders, &upgrade, &created); err != nil {
		return Profile{}, err
	}
	for text, dst := range map[string]any{res: &p.AllowedRes, srcs: &p.AllowedSources, pref: &p.PreferredWords, rej: &p.RejectedWords} {
		if err := json.Unmarshal([]byte(text), dst); err != nil {
			return Profile{}, err
		}
	}
	if p.AllowedRes == nil {
		p.AllowedRes = []int{}
	}
	if p.AllowedSources == nil {
		p.AllowedSources = []string{}
	}
	if p.PreferredWords == nil {
		p.PreferredWords = []string{}
	}
	if p.RejectedWords == nil {
		p.RejectedWords = []string{}
	}
	p.MinSizeMB, p.MaxSizeMB, p.MaxSizePerHourMB = optInt(minMB), optInt(maxMB), optInt(perHour)
	p.PreferHDR = hdr != 0
	if upgrade.Valid {
		p.UpgradeUntil = &upgrade.String
	}
	t, err := db.ParseTS(created)
	if err != nil {
		return Profile{}, err
	}
	p.CreatedAt = t
	return p, nil
}

func optInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func intOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// Profiles lists every profile: movies, then shows, then music; HD before 4K.
func (s *Store) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileCols+` FROM profiles
		ORDER BY CASE kind WHEN 'movie' THEN 0 WHEN 'show' THEN 1 ELSE 2 END, CASE quality WHEN 'hd' THEN 0 ELSE 1 END, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProfileByID returns one profile, or ErrNotFound.
func (s *Store) ProfileByID(ctx context.Context, id int64) (Profile, error) {
	p, err := scanProfile(s.db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// ProfileFor returns the profile of a kind and quality, or ErrNotFound.
func (s *Store) ProfileFor(ctx context.Context, kind, quality string) (Profile, error) {
	p, err := scanProfile(s.db.QueryRowContext(ctx,
		`SELECT `+profileCols+` FROM profiles WHERE kind = ? AND quality = ?`, kind, quality))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// UpdateProfile writes every editable column. A name used by another profile of
// the same kind gives ErrDuplicateName.
func (s *Store) UpdateProfile(ctx context.Context, p Profile) error {
	hdr := 0
	if p.PreferHDR {
		hdr = 1
	}
	var upgrade any
	if p.UpgradeUntil != nil {
		upgrade = *p.UpgradeUntil
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE profiles SET name = ?, allowed_res = ?, preferred_res = ?, allowed_sources = ?, min_size_mb = ?,
		        max_size_mb = ?, max_size_per_hour_mb = ?, prefer_hdr = ?, preferred_words = ?, rejected_words = ?,
		        min_seeders = ?, upgrade_until = ?
		 WHERE id = ?`,
		p.Name, jsonText(p.AllowedRes), p.PreferredRes, jsonText(p.AllowedSources), intOrNil(p.MinSizeMB),
		intOrNil(p.MaxSizeMB), intOrNil(p.MaxSizePerHourMB), hdr, jsonText(p.PreferredWords), jsonText(p.RejectedWords),
		p.MinSeeders, upgrade, p.ID)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return ErrDuplicateName
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- blocklist ---------------------------------------------------------------

// BlockEntry is one blocked release.
type BlockEntry struct {
	ID           int64     `json:"id"`
	TitleID      int64     `json:"titleId"`
	GUID         string    `json:"guid"`
	InfoHash     string    `json:"infoHash"`
	ReleaseTitle string    `json:"releaseTitle"`
	Reason       string    `json:"reason"`
	CreatedAt    time.Time `json:"createdAt"`
}

const blockCols = `id, title_id, guid, infohash, release_title, reason, created_at`

func scanBlock(s scanner) (BlockEntry, error) {
	var (
		b       BlockEntry
		created string
	)
	if err := s.Scan(&b.ID, &b.TitleID, &b.GUID, &b.InfoHash, &b.ReleaseTitle, &b.Reason, &created); err != nil {
		return BlockEntry{}, err
	}
	t, err := db.ParseTS(created)
	if err != nil {
		return BlockEntry{}, err
	}
	b.CreatedAt = t
	return b, nil
}

// AddBlock stores an entry and returns it with its id.
func (s *Store) AddBlock(ctx context.Context, b BlockEntry) (BlockEntry, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO blocklist (title_id, guid, infohash, release_title, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		b.TitleID, b.GUID, b.InfoHash, b.ReleaseTitle, b.Reason, db.TS(b.CreatedAt))
	if err != nil {
		return BlockEntry{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return BlockEntry{}, err
	}
	b.ID = id
	return b, nil
}

// Blocklist lists entries, newest first, with the total.
func (s *Store) Blocklist(ctx context.Context, limit, offset int) ([]BlockEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocklist`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+blockCols+` FROM blocklist ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []BlockEntry{}
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// BlocksForTitle lists every entry for one title.
func (s *Store) BlocksForTitle(ctx context.Context, titleID int64) ([]BlockEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+blockCols+` FROM blocklist WHERE title_id = ?`, titleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockEntry
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlock removes an entry.
func (s *Store) DeleteBlock(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM blocklist WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
