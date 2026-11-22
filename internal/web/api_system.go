package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/library"
)

// JobStatuser is the scheduler as the status page sees it.
type JobStatuser interface {
	Status() []jobs.Status
}

// License is Marquee's licence, shown on the About screen.
const License = "MIT"

func (s *Server) registerSystem(api *http.ServeMux) {
	// Owner and admins: it shows where the config folder is and what every
	// connected app last said, none of which a member needs.
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}
	api.HandleFunc("GET /api/v1/system/status", s.guarded(manage, s.handleSystemStatus))
}

type databaseStatus struct {
	Name      string `json:"name"` // main or events
	File      string `json:"file"`
	OK        bool   `json:"ok"`
	SizeBytes int64  `json:"sizeBytes"` // the file and its write-ahead log
}

type diskStatus struct {
	Path       string `json:"path"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type jobStatus struct {
	Name            string     `json:"name"`
	IntervalSeconds int64      `json:"intervalSeconds"`
	Running         bool       `json:"running"`
	Runs            int64      `json:"runs"`
	Failures        int64      `json:"failures"`
	LastRun         *time.Time `json:"lastRun"` // null until it has run
	LastDurationMs  int64      `json:"lastDurationMs"`
	LastError       string     `json:"lastError"`
}

// connectionStatus is one app Marquee talks to and how the last call went.
type connectionStatus struct {
	// Kind says what sort of connection it is: downloadClient, source, mediaServer,
	// metadata, alertChannel or plexWatchlist.
	Kind string `json:"kind"`
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name"`
	// App is the specific app, such as qbittorrent, torznab, jellyfin or tvmaze.
	App       string     `json:"app"`
	OK        bool       `json:"ok"`
	LastError string     `json:"lastError"`
	LastOKAt  *time.Time `json:"lastOkAt"`
}

type serverStatus struct {
	PublicURL      string   `json:"publicUrl"`
	TrustedProxies []string `json:"trustedProxies"`
	// TrustedFromEnvironment are the ones MARQUEE_TRUSTED_PROXIES names.
	TrustedFromEnvironment []string `json:"trustedFromEnvironment"`
}

type systemStatus struct {
	Version       string             `json:"version"`
	Go            string             `json:"go"`
	OS            string             `json:"os"`
	Arch          string             `json:"arch"`
	StartedAt     time.Time          `json:"startedAt"`
	UptimeSeconds int64              `json:"uptimeSeconds"`
	ConfigDir     string             `json:"configDir"`
	Databases     []databaseStatus   `json:"databases"`
	Disk          *diskStatus        `json:"disk"`
	Jobs          []jobStatus        `json:"jobs"`
	Connections   []connectionStatus `json:"connections"`
	Server        serverStatus       `json:"server"`
	Repository    string             `json:"repository"`
	License       string             `json:"license"`
}

// GET /api/v1/system/status: how Marquee is doing, for Settings > About. The public
// GET /api/v1/health stays the small one a monitor polls; this one is signed in,
// owner and admins only.
func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	out := systemStatus{
		Version: s.Version, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		StartedAt: s.Started.UTC(), UptimeSeconds: int64(time.Since(s.Started).Seconds()),
		ConfigDir: s.ConfigDir, Repository: outbound.ProjectURL, License: License,
		Databases: s.databaseStatuses(ctx), Jobs: s.jobStatuses(), Connections: s.connectionStatuses(ctx),
	}
	if s.ConfigDir != "" {
		if sp, err := library.DiskSpace(s.ConfigDir); err == nil {
			out.Disk = &diskStatus{Path: s.ConfigDir, FreeBytes: sp.Free, TotalBytes: sp.Total}
		}
	}
	cfg := s.runtime(ctx)
	out.Server = serverStatus{PublicURL: cfg.publicURL, TrustedProxies: nonNil(cfg.stored), TrustedFromEnvironment: make([]string, 0, len(s.envProxies))}
	for _, p := range s.envProxies {
		out.Server.TrustedFromEnvironment = append(out.Server.TrustedFromEnvironment, p.String())
	}
	writeJSON(w, http.StatusOK, out)
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Server) databaseStatuses(ctx context.Context) []databaseStatus {
	out := []databaseStatus{}
	add := func(name, file string, ok bool) {
		d := databaseStatus{Name: name, File: file, OK: ok}
		if s.ConfigDir != "" {
			d.SizeBytes = fileSize(filepath.Join(s.ConfigDir, file)) + fileSize(filepath.Join(s.ConfigDir, file+"-wal"))
		}
		out = append(out, d)
	}
	if s.MainDB != nil {
		add("main", db.MainFile, s.MainDB.PingContext(ctx) == nil)
	}
	if s.EventsDB != nil {
		add("events", db.EventsFile, s.EventsDB.PingContext(ctx) == nil)
	}
	return out
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

func (s *Server) jobStatuses() []jobStatus {
	out := []jobStatus{}
	if s.Jobs == nil {
		return out
	}
	for _, j := range s.Jobs.Status() {
		st := jobStatus{
			Name: j.Name, IntervalSeconds: int64(j.Interval / time.Second), Running: j.Running, Runs: j.Runs,
			Failures: j.Failures, LastDurationMs: j.Duration.Milliseconds(), LastError: j.LastErr,
		}
		if !j.LastRun.IsZero() {
			t := j.LastRun.UTC()
			st.LastRun = &t
		}
		out = append(out, st)
	}
	return out
}

// connectionStatuses lists every enabled connection and how its last call went.
// What a module cannot tell is left out rather than failing the page.
func (s *Server) connectionStatuses(ctx context.Context) []connectionStatus {
	out := []connectionStatus{}
	if s.Downloads != nil {
		if list, err := s.Downloads.Clients(ctx); err == nil {
			for _, c := range list {
				if c.Enabled {
					out = append(out, connectionStatus{Kind: "downloadClient", ID: c.ID, Name: c.Name, App: c.Kind,
						OK: c.LastError == "", LastError: c.LastError, LastOKAt: c.LastOKAt})
				}
			}
		}
	}
	if s.Sources != nil {
		if list, err := s.Sources.List(ctx); err == nil {
			for _, c := range list {
				if c.Enabled {
					out = append(out, connectionStatus{Kind: "source", ID: c.ID, Name: c.Name, App: c.Kind,
						OK: c.LastError == "", LastError: c.LastError, LastOKAt: c.LastOKAt})
				}
			}
		}
	}
	if s.MediaServers != nil {
		if list, err := s.MediaServers.List(ctx); err == nil {
			for _, c := range list {
				if c.Enabled {
					out = append(out, connectionStatus{Kind: "mediaServer", ID: c.ID, Name: c.Name, App: c.Kind,
						OK: c.LastError == "", LastError: c.LastError, LastOKAt: c.LastOKAt})
				}
			}
		}
	}
	if s.Metadata != nil {
		rep := s.Metadata.Status(ctx)
		names := make([]string, 0, len(rep.Providers))
		for name := range rep.Providers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if name == "tmdb" && !rep.HasKey {
				continue // not set up: nothing to report
			}
			p := rep.Providers[name]
			out = append(out, connectionStatus{Kind: "metadata", Name: name, App: name,
				OK: p.LastError == "", LastError: p.LastError, LastOKAt: p.LastOK})
		}
	}
	if s.Alerts != nil {
		if list, err := s.Alerts.List(ctx); err == nil {
			for _, c := range list {
				if c.Enabled {
					out = append(out, connectionStatus{Kind: "alertChannel", ID: c.ID, Name: c.Name, App: c.Kind,
						OK: c.LastError == "", LastError: c.LastError, LastOKAt: c.LastOKAt})
				}
			}
		}
	}
	if s.Watchlists != nil {
		if st, err := s.Watchlists.Status(ctx); err == nil && st.Linked && st.Enabled {
			out = append(out, connectionStatus{Kind: "plexWatchlist", Name: "Plex watchlists", App: "plex",
				OK: st.LastError == "", LastError: st.LastError, LastOKAt: st.LastCheckedAt})
		}
	}
	return out
}
