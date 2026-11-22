//go:build ignore

// gen-api-doc lists the routes Marquee registers, by reading the source of
// internal/web with go/parser. It imports nothing from the app, and it changes
// nothing but the file you point -write at.
//
// Usage (from the repository root):
//
//	go run scripts/gen-api-doc.go                          print the table
//	go run scripts/gen-api-doc.go -write docs/api.md       refresh the table inside docs/api.md
//	go run scripts/gen-api-doc.go -check docs/api.md       exit 1 if docs/api.md is out of date
//
// The table goes between the markers
//
//	<!-- routes:start -->
//	<!-- routes:end -->
//
// Who may call a route comes from the guard the handler is wrapped in (roles and
// the API-key permission it needs). The one-line purposes are in the purposes
// table below; a route with no entry gets its handler's name and a warning on
// stderr, so a new endpoint is never silently left out of the docs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	startMarker = "<!-- routes:start -->"
	endMarker   = "<!-- routes:end -->"
)

// groups orders the sections and names them by the file that registers the routes.
var groups = []struct{ file, title string }{
	{"server.go", "Sign-in, people and settings"},
	{"api_titles.go", "Search, titles and artwork"},
	{"api_library.go", "Library folders and files"},
	{"api_library_files.go", "Library file actions"},
	{"api_downloads.go", "Downloads and download apps"},
	{"api_requests.go", "Requests, people and limits"},
	{"api_sources.go", "Sources, quality rules and release search"},
	{"api_import.go", "Import and history"},
	{"api_mediaservers.go", "Media servers"},
	{"api_watchlists.go", "Plex watchlists"},
	{"api_calendar.go", "Calendar"},
	{"api_alerts.go", "Alerts"},
	{"api_mcp.go", "Claude (MCP)"},
	{"api_system.go", "System status"},
}

// purposes are the one-line descriptions, keyed "METHOD path" as registered.
var purposes = map[string]string{
	"GET /api/v1/health":                               "Health check: status, version, uptime and database.",
	"GET /api/v1/setup":                                "Says whether first-run setup (creating the owner) is still needed.",
	"POST /api/v1/setup/owner":                         "First run: create the owner. Only works while there is no owner.",
	"POST /api/v1/auth/login":                          "Sign in with a name and password; sets the session cookie.",
	"POST /api/v1/invites/accept":                      "Accept an invite and create an account.",
	"POST /api/v1/auth/logout":                         "Sign out and clear the session cookie.",
	"GET /api/v1/me":                                   "Who you are, how you signed in and which permissions apply.",
	"GET /api/v1/events":                               "Live updates as a Server-Sent Events stream (downloads, requests, library).",
	"POST /api/v1/invites":                             "Create a one-time invite for a new admin or member.",
	"GET /api/v1/apikeys":                              "List your API keys and the permissions you may put on one.",
	"POST /api/v1/apikeys":                             "Create an API key. The secret is shown once.",
	"DELETE /api/v1/apikeys/{id}":                      "Revoke an API key.",
	"GET /api/v1/settings/{section}":                   "Read one settings section, with secrets masked.",
	"PUT /api/v1/settings/{section}":                   "Replace one settings section; a masked secret keeps its stored value.",
	"GET /api/v1/search":                               "Look up movies, shows and artists at the metadata providers. Saves nothing.",
	"GET /api/v1/titles":                               "List tracked titles, with what is on disk for each.",
	"POST /api/v1/titles":                              "Add a title to the catalog from a provider id.",
	"GET /api/v1/titles/{id}":                          "One title with its episodes or albums and its files.",
	"PATCH /api/v1/titles/{id}":                        "Turn monitoring on or off for a title.",
	"DELETE /api/v1/titles/{id}":                       "Remove a title from the catalog. Never deletes files.",
	"POST /api/v1/titles/{id}/refresh":                 "Fetch the title's details from the provider again.",
	"GET /api/v1/metadata/status":                      "Whether a TMDB key is set, and how each provider last answered.",
	"GET /api/v1/artwork/{provider}/{rest...}":         "Posters, backdrops and covers, cached by Marquee (the only way the UI loads images).",
	"GET /api/v1/library/folders":                      "List library folders.",
	"POST /api/v1/library/folders":                     "Add a library folder (movie, show or music).",
	"DELETE /api/v1/library/folders/{id}":              "Remove a library folder from Marquee. Files on disk are not touched.",
	"POST /api/v1/library/scan":                        "Start scanning one folder or all of them; progress arrives on the event stream.",
	"GET /api/v1/library/files":                        "List files the scanner found, by match state.",
	"GET /api/v1/library/stats":                        "Counts and sizes for the library.",
	"POST /api/v1/library/files/{id}/rename":           "Give a file the name the importer would give it today.",
	"DELETE /api/v1/library/files/{id}":                "Take a file out of the library; deleteFile=true also deletes it from disk.",
	"GET /api/v1/downloads":                            "List downloads (active, history or all).",
	"GET /api/v1/downloads/summary":                    "Counts of downloads by state, and download speed.",
	"POST /api/v1/downloads/{id}/pause":                "Pause a download in its client.",
	"POST /api/v1/downloads/{id}/resume":               "Resume a download in its client.",
	"DELETE /api/v1/downloads/{id}":                    "Remove a download from its client (optionally with its data).",
	"POST /api/v1/downloads/{id}/import":               "Run the import again for a finished or failed download.",
	"GET /api/v1/import/status":                        "The importer's last 20 results and what it is working on.",
	"GET /api/v1/clients":                              "List download apps (qBittorrent, Deluge, SABnzbd); secrets masked.",
	"POST /api/v1/clients":                             "Add a download app.",
	"PUT /api/v1/clients/{id}":                         "Change a download app; fields left out keep their value.",
	"DELETE /api/v1/clients/{id}":                      "Remove a download app.",
	"POST /api/v1/clients/{id}/test":                   "Test the connection to a download app.",
	"POST /api/v1/requests":                            "Ask for a title in HD or 4K, as the caller.",
	"GET /api/v1/requests":                             "List asks. Members only ever see their own.",
	"GET /api/v1/requests/summary":                     "Counts of asks by state.",
	"GET /api/v1/requests/{id}":                        "One ask.",
	"POST /api/v1/requests/{id}/approve":               "Approve an ask (optionally always allow that person 4K).",
	"POST /api/v1/requests/{id}/decline":               "Decline an ask, with an optional reason.",
	"DELETE /api/v1/requests/{id}":                     "Cancel your own ask.",
	"GET /api/v1/people":                               "People with their weekly use and limits.",
	"PUT /api/v1/people/{userId}/limits":               "Set a person's weekly limits and auto-approve rules.",
	"GET /api/v1/sources":                              "List sources (Torznab and Newznab feeds); keys masked.",
	"POST /api/v1/sources":                             "Add a source.",
	"POST /api/v1/sources/import":                      "Import every indexer from a Prowlarr or Jackett.",
	"PUT /api/v1/sources/{id}":                         "Change a source; fields left out keep their value.",
	"DELETE /api/v1/sources/{id}":                      "Remove a source.",
	"POST /api/v1/sources/{id}/test":                   "Test a source and read what it supports.",
	"GET /api/v1/profiles":                             "List quality profiles.",
	"PUT /api/v1/profiles/{id}":                        "Change a quality profile.",
	"GET /api/v1/blocklist":                            "List releases Marquee will not pick again.",
	"DELETE /api/v1/blocklist/{id}":                    "Take a release off the blocklist.",
	"GET /api/v1/titles/{id}/releases":                 "Search all sources now and rank the releases, with reasons.",
	"POST /api/v1/titles/{id}/grab":                    "Send one chosen release to a download app.",
	"GET /api/v1/titles/{id}/history":                  "A title's history, newest first.",
	"GET /api/v1/titles/{id}/open":                     "The address to open the title on a media server.",
	"GET /api/v1/mediaservers":                         "List media servers (Jellyfin, Emby, Plex); tokens masked.",
	"POST /api/v1/mediaservers":                        "Add a media server.",
	"PUT /api/v1/mediaservers/{id}":                    "Change a media server; fields left out keep their value.",
	"DELETE /api/v1/mediaservers/{id}":                 "Remove a media server.",
	"POST /api/v1/mediaservers/{id}/test":              "Test a media server and list its libraries.",
	"GET /api/v1/watchlists":                           "Plex link status, friends and the last check.",
	"POST /api/v1/watchlists/plex/link":                "Start linking the owner's Plex account (a PIN).",
	"GET /api/v1/watchlists/plex/link/{pinId}":         "Check whether the Plex link was approved.",
	"DELETE /api/v1/watchlists/plex/link":              "Unlink the Plex account.",
	"PUT /api/v1/watchlists/plex/friends/{plexUserId}": "Link or unlink a Plex friend and a Marquee person.",
	"POST /api/v1/watchlists/check":                    "Check the Plex watchlists now.",
	"PUT /api/v1/watchlists/plex":                      "Change how Plex watchlists are read: on or off, friends, auto-created members, quality.",
	"GET /api/v1/system/status":                        "How Marquee is doing: version, config folder, databases, disk, jobs and connections (the About screen).",
	"GET /api/v1/calendar":                             "Upcoming episodes and releases (at most 92 days at a time).",
	"GET /api/v1/alerts":                               "List alert channels; secrets masked.",
	"POST /api/v1/alerts":                              "Add an alert channel (Discord, ntfy, email and others).",
	"PUT /api/v1/alerts/{id}":                          "Change an alert channel.",
	"DELETE /api/v1/alerts/{id}":                       "Remove an alert channel.",
	"POST /api/v1/alerts/{id}/test":                    "Send a test message to a channel.",
	"GET /api/v1/mcp/info":                             "Whether Claude's endpoint is on, its address, its tools and recent calls.",
	"ANY /mcp":                                         "The MCP endpoint (Streamable HTTP, JSON-RPC). Bearer API key only.",
	"ANY /api/v1/mcp":                                  "Same endpoint under the API prefix.",
}

// route is one registered route.
type route struct {
	file    string
	pos     int
	method  string
	path    string
	who     string
	handler string
}

func main() {
	dir := flag.String("dir", "internal/web", "folder with the web package's source")
	write := flag.String("write", "", "rewrite the table between the markers in this file")
	check := flag.String("check", "", "exit 1 if the table in this file is out of date")
	flag.Parse()

	routes, err := collect(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-api-doc:", err)
		os.Exit(1)
	}
	table := render(routes)

	switch {
	case *write != "":
		old, err := os.ReadFile(*write)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-api-doc:", err)
			os.Exit(1)
		}
		updated, err := splice(old, table)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-api-doc:", *write+":", err)
			os.Exit(1)
		}
		if err := os.WriteFile(*write, updated, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen-api-doc:", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "gen-api-doc: %d routes written to %s\n", len(routes), *write)
	case *check != "":
		old, err := os.ReadFile(*check)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-api-doc:", err)
			os.Exit(1)
		}
		updated, err := splice(old, table)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-api-doc:", *check+":", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(old), normalize(updated)) {
			fmt.Fprintf(os.Stderr, "gen-api-doc: %s is out of date; run: go run scripts/gen-api-doc.go -write %s\n", *check, *check)
			os.Exit(1)
		}
	default:
		fmt.Print(table)
	}
}

func normalize(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// splice puts table between the markers of doc.
func splice(doc []byte, table string) ([]byte, error) {
	text := string(normalize(doc))
	i := strings.Index(text, startMarker)
	j := strings.Index(text, endMarker)
	if i < 0 || j < 0 || j < i {
		return nil, fmt.Errorf("the markers %s and %s were not found, in that order", startMarker, endMarker)
	}
	return []byte(text[:i+len(startMarker)] + "\n\n" + table + "\n" + text[j:]), nil
}

// ---- reading the source ----------------------------------------------------------

func collect(dir string) ([]route, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var routes []route
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			routes = append(routes, routesIn(name, fn)...)
		}
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("no routes found in %s (run this from the repository root)", dir)
	}
	return routes, nil
}

// routesIn finds the HandleFunc and Handle calls in one function, with the guard
// variables defined in it.
func routesIn(file string, fn *ast.FuncDecl) []route {
	guards := map[string]string{}     // variable -> who may call
	roleVars := map[string][]string{} // variable -> roles (a []accounts.Role)

	// First pass: local variables.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		cl, ok2 := as.Rhs[0].(*ast.CompositeLit)
		if !ok || !ok2 {
			return true
		}
		switch t := cl.Type.(type) {
		case *ast.Ident:
			if t.Name == "guard" {
				guards[id.Name] = describeGuard(cl, roleVars)
			}
		case *ast.ArrayType:
			roleVars[id.Name] = roleNames(cl)
		}
		return true
	})

	// Second pass: registrations.
	var out []route
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		method, path := "ANY", pattern
		if m, p, found := strings.Cut(pattern, " "); found {
			method, path = m, strings.TrimSpace(p)
		}
		if path == "/" || strings.HasSuffix(path, "/") { // catch-alls: the UI and "no such endpoint"
			return true
		}

		r := route{file: file, pos: int(call.Pos()), method: method, path: path}
		r.who, r.handler = whoAndHandler(call, sel.Sel.Name, guards, roleVars)
		out = append(out, r)
		return true
	})
	return out
}

// whoAndHandler reads the second argument of a registration.
func whoAndHandler(call *ast.CallExpr, kind string, guards map[string]string, roleVars map[string][]string) (who, handler string) {
	arg := call.Args[1]
	if g, ok := arg.(*ast.CallExpr); ok {
		if gs, ok := g.Fun.(*ast.SelectorExpr); ok && gs.Sel.Name == "guarded" && len(g.Args) == 2 {
			switch ga := g.Args[0].(type) {
			case *ast.Ident:
				who = guards[ga.Name]
			case *ast.CompositeLit:
				who = describeGuard(ga, roleVars)
			}
			return who, exprName(g.Args[1])
		}
	}
	if kind == "Handle" {
		return "Bearer API key only (no browser session)", exprName(arg)
	}
	return "Anyone, no sign-in", exprName(arg)
}

func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.Ident:
		return x.Name
	case *ast.FuncLit:
		return "(inline)"
	}
	return "?"
}

// describeGuard turns a guard{...} literal into words.
func describeGuard(cl *ast.CompositeLit, roleVars map[string][]string) string {
	var roles []string
	scope := ""
	sessionOnly := false
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "roles":
			switch v := kv.Value.(type) {
			case *ast.CompositeLit:
				roles = roleNames(v)
			case *ast.Ident:
				roles = roleVars[v.Name]
			}
		case "scope":
			if s, ok := kv.Value.(*ast.SelectorExpr); ok {
				scope = strings.ToLower(strings.TrimPrefix(s.Sel.Name, "Scope"))
			}
		case "sessionOnly":
			if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "true" {
				sessionOnly = true
			}
		}
	}

	var who string
	switch {
	case len(roles) == 0:
		who = "Any signed-in person"
	case len(roles) == 1:
		who = capitalize(roles[0]) + " only"
	default:
		who = capitalize(strings.Join(roles, " or "))
	}
	switch {
	case sessionOnly:
		who += ", browser sign-in only (not an API key)"
	case scope != "":
		who += "; a key needs `" + scope + "`"
	}
	return who
}

func roleNames(cl *ast.CompositeLit) []string {
	var out []string
	for _, el := range cl.Elts {
		if s, ok := el.(*ast.SelectorExpr); ok {
			out = append(out, strings.ToLower(strings.TrimPrefix(s.Sel.Name, "Role")))
		}
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// ---- writing the table ---------------------------------------------------------------

func render(routes []route) string {
	byFile := map[string][]route{}
	for _, r := range routes {
		byFile[r.file] = append(byFile[r.file], r)
	}
	var order []string
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, g := range groups {
		titles[g.file] = g.title
		if _, ok := byFile[g.file]; ok {
			order = append(order, g.file)
			seen[g.file] = true
		}
	}
	var rest []string
	for f := range byFile {
		if !seen[f] {
			rest = append(rest, f)
			titles[f] = titleFromFile(f)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	var b strings.Builder
	b.WriteString("_Generated by `go run scripts/gen-api-doc.go -write docs/api.md`. Do not edit between the markers._\n")
	for _, f := range order {
		rs := byFile[f]
		sort.Slice(rs, func(i, j int) bool { return rs[i].pos < rs[j].pos })
		fmt.Fprintf(&b, "\n### %s\n\n| Method | Path | Who may call it | What it does |\n|---|---|---|---|\n", titles[f])
		for _, r := range rs {
			purpose, ok := purposes[r.method+" "+r.path]
			if !ok {
				purpose = humanize(r.handler)
				fmt.Fprintf(os.Stderr, "gen-api-doc: no purpose written for %s %s (handler %s); add it to scripts/gen-api-doc.go\n", r.method, r.path, r.handler)
			}
			fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", r.method, r.path, r.who, purpose)
		}
	}
	return b.String()
}

func titleFromFile(f string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(f, "api_"), ".go")
	return capitalize(strings.ReplaceAll(s, "_", " "))
}

// humanize turns handleTitleGrab into "Title grab" (the fallback purpose).
func humanize(h string) string {
	h = strings.TrimPrefix(h, "handle")
	var words []string
	cur := []rune{}
	for _, r := range h {
		if unicode.IsUpper(r) && len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		words = append(words, strings.ToLower(string(cur)))
	}
	if len(words) == 0 {
		return "(undocumented)"
	}
	return capitalize(strings.Join(words, " ")) + " (undocumented)"
}
