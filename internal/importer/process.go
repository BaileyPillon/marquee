package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/pathmap"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/grab"
	"github.com/BaileyPillon/marquee/internal/history"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Placed is a file the importer put in the library.
type Placed struct {
	Path    string `json:"path"` // under the library folder
	Mode    string `json:"mode"` // hardlink, copy or move
	Quality string `json:"quality"`
	// Upgrade is set when the file replaced a worse one; From is that one's
	// quality and Replaced the path that was removed.
	Upgrade  bool   `json:"upgrade,omitempty"`
	From     string `json:"from,omitempty"`
	Replaced string `json:"replaced,omitempty"`
}

// Result is what one import did.
type Result struct {
	DownloadID int64
	ImportID   int64
	Outcome    string // OutcomeImported, OutcomeSkipped or OutcomeFailed
	Reason     string // why it failed (or a note on why nothing was added)
	Imported   []Placed
	Skipped    []skip
	// Blocked: the release was put on the blocklist. Removed: it was taken out of
	// the client with its data (a fake or a sample). Searching: the copy will be
	// searched again.
	Blocked   bool
	Removed   bool
	Searching bool
	// Retryable: the failure was not the release's fault, so trying again later
	// may work.
	Retryable bool
}

// run holds one import while it is going on.
type run struct {
	d      downloads.Download
	cp     grab.Copy
	det    *titles.Detail
	folder library.Folder
	res    Result

	blame   bool // the release is bad: blocklist it, search again
	noMedia bool // nothing usable in it: take it out of the client too

	adopted    []plan // already in place
	placed     []plan
	removedAbs []string // files an upgrade removed (full paths)
	covered    map[int64]bool
	srcPath    string
	srcIsDir   bool
	stage      string
}

var reHexHash = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// process runs one import from start to finish. retry lets it begin from a
// failed or abandoned download, not just a completed one. The returned error is
// only for "could not even start" (no such download, wrong state).
func (s *Service) process(ctx context.Context, id int64, retry, manual bool) (Result, error) {
	d, err := s.deps.Downloads.Download(ctx, id)
	if err != nil {
		return Result{}, err
	}
	switch d.State {
	case downloads.StateCompleted:
	case downloads.StateFailed, downloads.StateImporting:
		if !retry {
			return Result{}, ErrWrongState
		}
	default:
		return Result{}, ErrWrongState
	}
	impID, err := s.store.begin(ctx, importStart{
		DownloadID: d.ID, TitleID: d.TitleID, CopyID: d.CopyID, ReleaseTitle: d.ReleaseTitle, Manual: manual, At: s.now(),
	})
	if err != nil {
		return Result{}, err
	}
	if err := s.deps.Downloads.SetState(ctx, id, downloads.StateImporting, ""); err != nil {
		_ = s.store.finish(context.WithoutCancel(ctx), impID, importEnd{Outcome: OutcomeFailed, Reason: err.Error(), At: s.now()})
		return Result{}, err
	}

	r := &run{d: d, covered: map[int64]bool{}}
	r.res.DownloadID, r.res.ImportID = id, impID
	s.attempt(ctx, r)
	s.conclude(context.WithoutCancel(ctx), r)
	return r.res, nil
}

// failWith ends an attempt as failed. blame says the release is at fault.
func (r *run) failWith(blame, noMedia bool, format string, args ...any) {
	r.res.Outcome = OutcomeFailed
	r.res.Reason = fmt.Sprintf(format, args...)
	r.blame, r.noMedia = blame, noMedia
	r.res.Retryable = !blame
}

// step says which step of the import is running and, when it is known, where the
// files are going. The Downloads screen shows it; a failure to say it is only
// logged.
func (s *Service) step(ctx context.Context, r *run, step, target string) {
	if err := s.deps.Downloads.SetImportStep(context.WithoutCancel(ctx), r.d.ID, step, target); err != nil {
		s.log.Warn("import: could not record the import step", "download", r.d.ID, "step", step, "err", err)
	}
}

// importTarget is where a set of planned files is going: the file itself when
// there is one, otherwise the folder they all share.
func importTarget(root string, plans []plan) string {
	switch len(plans) {
	case 0:
		return ""
	case 1:
		return filepath.Join(root, filepath.FromSlash(plans[0].dest))
	}
	common := path.Dir(plans[0].dest)
	for _, p := range plans[1:] {
		dir := path.Dir(p.dest)
		for common != "." && dir != common && !strings.HasPrefix(dir+"/", common+"/") {
			common = path.Dir(common)
		}
	}
	if common == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(common))
}

// attempt does the work of an import and leaves its verdict in r.res.
func (s *Service) attempt(ctx context.Context, r *run) {
	d := r.d
	s.step(ctx, r, downloads.StepChecking, "")
	if d.CopyID == 0 {
		r.failWith(false, false, "This download is not tied to a copy of a title, so there is no library folder to put it in.")
		return
	}
	cp, err := s.deps.Copies.Copy(ctx, d.CopyID)
	if err != nil {
		r.failWith(false, false, "The copy this download was for could not be read: %s.", plainErr(err))
		return
	}
	r.cp = cp
	if cp.FolderID == 0 || cp.FolderPath == "" {
		r.failWith(false, false, "The copy has no library folder. Add one in Settings, then import again.")
		return
	}
	if st, err := os.Stat(cp.FolderPath); err != nil || !st.IsDir() {
		r.failWith(false, false, "The library folder %s cannot be reached.", cp.FolderPath)
		return
	}
	folder, err := s.deps.Library.Folder(ctx, cp.FolderID)
	if err != nil {
		r.failWith(false, false, "The library folder could not be read: %s.", plainErr(err))
		return
	}
	r.folder = folder
	det, err := s.deps.Titles.Get(ctx, d.TitleID)
	if err != nil {
		r.failWith(false, false, "The title this download was for could not be read: %s.", plainErr(err))
		return
	}
	r.det = det
	if libraryKind(det.Kind) != folder.Kind {
		r.failWith(false, false, "%q is a %s but its folder is for %ss.", det.Name, det.Kind, folder.Kind)
		return
	}

	src, err := s.resolveSource(ctx, d)
	if err != nil {
		r.failWith(false, false, "%s", err.Error())
		return
	}
	r.srcPath = src
	if pathmap.Within(folder.Path, src) || pathmap.Within(src, folder.Path) {
		r.failWith(false, false, "The download's folder and the library folder overlap, so Marquee will not touch it.")
		return
	}

	files, root, stage, err := s.collect(ctx, r, src, folder.Kind)
	r.stage = stage
	if stage != "" {
		defer os.RemoveAll(stage)
	}
	if err != nil {
		r.failWith(true, false, "%s", err.Error())
		return
	}
	if len(files) == 0 {
		r.failWith(true, true, "No usable media files in the download (only samples, extras or junk).")
		return
	}

	det = s.ensureTracks(ctx, r, det)
	v := newVerifier(det, root, d.ReleaseTitle, d.EpisodeIDs)
	plans, skips := v.verify(files)
	r.res.Skipped = skips
	if len(plans) == 0 {
		r.failWith(true, false, "The files in the download are not %q: %s", det.Name, summarize(skips))
		return
	}

	s.step(ctx, r, downloads.StepRenaming, importTarget(folder.Path, plans))
	prof := s.profileFor(ctx, det.Kind, cp.Quality)
	existing := s.existingFiles(ctx, folder.ID, det.ID)
	method := MethodLink
	if d.Protocol == grab.ProtocolUsenet {
		method = MethodMove
	}

	s.step(ctx, r, downloads.StepMoving, "")
	var (
		ioErr      error
		registered []library.ImportedFile
	)
	for _, p := range plans {
		if ctx.Err() != nil {
			r.failWith(false, false, "Marquee was shut down before the import finished.")
			return
		}
		destAbs := filepath.Join(folder.Path, filepath.FromSlash(p.dest))
		act := judge(p, destAbs, existing, prof, det.Kind)
		switch act.kind {
		case actSkip:
			r.res.Skipped = append(r.res.Skipped, skip{File: p.src.Rel, Reason: act.note, Existing: true})
			r.cover(p)
			continue
		case actAdopt:
			r.adopted = append(r.adopted, p)
			r.cover(p)
			registered = append(registered, library.ImportedFile{RelPath: p.dest, TitleID: p.titleID, EpisodeIDs: p.episodes, TrackID: p.trackID, Quality: p.quality})
			continue
		}
		m := method
		if p.src.Owned {
			m = MethodMove
		}
		mode, err := transfer(ctx, p.src.Path, destAbs, m)
		if err != nil {
			if ioErr == nil {
				ioErr = err
			}
			r.res.Skipped = append(r.res.Skipped, skip{File: p.src.Rel, Reason: "Could not put it in the library: " + err.Error()})
			continue
		}
		placed := Placed{Path: p.dest, Mode: mode, Quality: p.quality}
		for _, old := range act.replace {
			oldAbs := filepath.Join(folder.Path, filepath.FromSlash(old.RelPath))
			if err := os.Remove(oldAbs); err != nil && !isNotExist(err) {
				s.log.Warn("upgrade: could not remove the old file", "path", oldAbs, "err", err)
				continue
			}
			if err := s.deps.Library.ForgetFiles(ctx, folder.ID, []string{old.RelPath}); err != nil {
				s.log.Warn("upgrade: could not forget the old file", "path", old.RelPath, "err", err)
			}
			pruneDir(folder.Path, filepath.Dir(oldAbs))
			r.removedAbs = append(r.removedAbs, oldAbs)
			existing = dropFile(existing, old.RelPath)
			placed.Upgrade, placed.From, placed.Replaced = true, old.Quality, old.RelPath
		}
		r.placed = append(r.placed, p)
		r.res.Imported = append(r.res.Imported, placed)
		r.cover(p)
		registered = append(registered, library.ImportedFile{RelPath: p.dest, TitleID: p.titleID, EpisodeIDs: p.episodes, TrackID: p.trackID, Quality: p.quality})
	}

	if len(registered) > 0 {
		if err := s.deps.Library.ImportFiles(ctx, folder.ID, registered); err != nil {
			// The files are in place; the next scan will find them.
			s.log.Warn("import: could not record the new files in the library", "download", d.ID, "err", err)
		}
	}

	switch {
	case len(r.res.Imported) > 0 || len(r.adopted) > 0:
		r.res.Outcome = OutcomeImported
	case ioErr != nil:
		r.failWith(false, false, "Marquee could not copy the files into the library: %s", plainErr(ioErr))
	case onlyExisting(r.res.Skipped):
		r.res.Outcome = OutcomeSkipped
		r.res.Reason = "The library already has " + whatWord(det.Kind) + "."
	default:
		r.failWith(true, false, "None of the files could be imported: %s", summarize(r.res.Skipped))
	}
	if r.res.Outcome != OutcomeFailed && d.Protocol == grab.ProtocolUsenet {
		s.cleanupSource(ctx, r)
	}
}

func whatWord(kind string) string {
	switch kind {
	case titles.KindShow:
		return "these episodes at the same or better quality"
	case titles.KindArtist:
		return "these tracks"
	}
	return "this film at the same or better quality"
}

// cover notes that a plan's episodes are now in the library (placed, or
// already there).
func (r *run) cover(p plan) {
	for _, id := range p.episodes {
		r.covered[id] = true
	}
}

func onlyExisting(skips []skip) bool {
	if len(skips) == 0 {
		return false
	}
	for _, s := range skips {
		if !s.Existing {
			return false
		}
	}
	return true
}

// summarize joins the first few reasons into one sentence.
func summarize(skips []skip) string {
	var parts []string
	seen := map[string]bool{}
	for _, s := range skips {
		if seen[s.Reason] {
			continue
		}
		seen[s.Reason] = true
		parts = append(parts, s.Reason)
		if len(parts) == 3 {
			break
		}
	}
	out := strings.Join(parts, "; ")
	if len(seen) > 3 {
		out += "; and more"
	}
	return out
}

func plainErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && len(msg) > 200 {
		msg = msg[i+2:]
	}
	return msg
}

// ---- finding the files ----------------------------------------------------------

// resolveSource finds the download on this machine: the client's content path,
// turned into Marquee's view of it by the client's path rules.
func (s *Service) resolveSource(ctx context.Context, d downloads.Download) (string, error) {
	raw := strings.TrimSpace(d.ContentPath)
	if raw == "" {
		return "", errors.New("The download client did not say where it put the files.")
	}
	var rules []pathmap.Rule
	if cp, err := s.deps.Downloads.ClientPaths(ctx, d.ClientID); err == nil {
		rules = cp.PathMap
	}
	cands := []string{pathmap.Map(rules, raw)}
	if cands[0] != raw {
		cands = append(cands, raw)
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return filepath.Clean(c), nil
		}
	}
	return "", fmt.Errorf("Marquee cannot see the finished download at %s. If the download client runs in a container or on another machine, add a path rule to the client in Settings.", raw)
}

// collect lists the media files of a download. When it has none of its own and
// there are archives, they are unpacked into a folder next to the download (the
// returned stage, to be removed by the caller) and those files are used, marked
// as Owned. root is the download's own folder, "" when it is a single file.
func (s *Service) collect(ctx context.Context, r *run, src, kind string) (files []srcFile, root, stage string, err error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, "", "", fmt.Errorf("Marquee cannot read the download: %v", err)
	}
	r.srcIsDir = st.IsDir()
	if st.IsDir() {
		root = src
		files = walkMedia(src, kind)
	} else if library.WantMedia(filepath.Base(src), kind) {
		files = []srcFile{{Path: src, Rel: filepath.Base(src), Size: st.Size()}}
	}
	if len(files) > 0 {
		return files, root, "", nil
	}
	if len(findArchives(src)) == 0 {
		return nil, root, "", nil
	}

	stage, err = makeStage(src, r.d.ID)
	if err != nil {
		return nil, root, "", fmt.Errorf("Marquee cannot make a folder to unpack into: %v", err)
	}
	extracted, err := extractArchives(ctx, src, stage, kind)
	if err != nil {
		return nil, root, stage, fmt.Errorf("The archive could not be unpacked: %v", err)
	}
	for _, p := range extracted {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		rel := relSlash(stage, p)
		if i := strings.IndexByte(rel, '/'); i >= 0 && isArchiveStaging(rel[:i]) {
			rel = rel[i+1:]
		}
		files = append(files, srcFile{Path: p, Rel: rel, Size: info.Size(), Owned: true})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, stage, stage, nil
}

// makeStage makes the folder archives are unpacked into: next to the download,
// so it is on the same disk (and so unpacked files can be moved, not copied).
func makeStage(src string, id int64) (string, error) {
	dir := filepath.Join(filepath.Dir(src), fmt.Sprintf(".marquee-extract-%d", id))
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err == nil {
		return dir, nil
	}
	return os.MkdirTemp("", "marquee-extract-")
}

func relSlash(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		rel = p
	}
	return filepath.ToSlash(rel)
}

// walkMedia lists the media files under root the way the library scanner would:
// no hidden files, samples, extras or housekeeping folders.
func walkMedia(root, kind string) []srcFile {
	var out []srcFile
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && library.SkipFolderName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !library.WantMedia(d.Name(), kind) {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		out = append(out, srcFile{Path: p, Rel: relSlash(root, p), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ---- what the library already has -------------------------------------------------

func (s *Service) existingFiles(ctx context.Context, folderID, titleID int64) []library.File {
	var out []library.File
	for off := 0; ; off += 1000 {
		files, total, err := s.deps.Library.Files(ctx, library.FileFilter{FolderID: folderID, TitleID: titleID, Limit: 1000, Offset: off})
		if err != nil {
			s.log.Warn("import: could not list the title's files", "title", titleID, "err", err)
			return out
		}
		for _, f := range files {
			if f.MissingSince == nil && f.State == library.StateMatched {
				out = append(out, f)
			}
		}
		if len(files) == 0 || off+len(files) >= total {
			return out
		}
	}
}

func dropFile(list []library.File, rel string) []library.File {
	out := list[:0:0]
	for _, f := range list {
		if f.RelPath != rel {
			out = append(out, f)
		}
	}
	return out
}

func (s *Service) profileFor(ctx context.Context, kind, quality string) decide.Profile {
	if s.deps.Profiles != nil {
		if p, err := s.deps.Profiles.Profile(ctx, kind, quality); err == nil {
			return p
		}
	}
	return decide.Profile{}
}

// Actions judge can choose.
const (
	actPlace = iota // put the file in the library
	actAdopt        // the same file is already in place: only record it
	actSkip         // leave the download's file alone
)

type action struct {
	kind    int
	note    string
	replace []library.File // worse files an upgrade replaces
}

// judge decides what to do with a verified file given what the library holds
// for the title: put it there, adopt the identical file already there, skip it
// because the library has the same or better, or upgrade a worse one.
func judge(p plan, destAbs string, existing []library.File, prof decide.Profile, kind string) action {
	if st, err := os.Stat(destAbs); err == nil {
		if st.Size() == p.src.Size {
			return action{kind: actAdopt}
		}
		return action{kind: actSkip, note: "A different file with the same name is already in the library, so this one was left out"}
	}
	if kind == titles.KindArtist {
		return action{kind: actPlace}
	}
	var conflicts []library.File
	for _, e := range existing {
		if strings.EqualFold(e.RelPath, p.dest) {
			continue
		}
		if kind == titles.KindShow && !overlap(e.EpisodeIDs, p.episodes) {
			continue
		}
		conflicts = append(conflicts, e)
	}
	if len(conflicts) == 0 {
		return action{kind: actPlace}
	}
	newRank := prof.RankRelease(p.parsed)
	var cutoff = -1
	if prof.UpgradeUntil != nil {
		cutoff = prof.RankRelease(parse.ParseRelease(*prof.UpgradeUntil))
	}
	for _, e := range conflicts {
		have := e.Quality
		if have == "" {
			have = "a file"
		}
		oldRank := prof.RankRelease(parse.ParseRelease(path.Base(e.RelPath)))
		switch {
		case newRank <= oldRank:
			return action{kind: actSkip, note: fmt.Sprintf("The library already has %s, which ranks the same or better than %s", have, p.quality)}
		case cutoff >= 0 && oldRank >= cutoff:
			return action{kind: actSkip, note: fmt.Sprintf("The library already has %s, which meets your profile's upgrade limit", have)}
		case kind == titles.KindShow && !subset(e.EpisodeIDs, p.episodes):
			return action{kind: actSkip, note: "The library has a file for these episodes that also holds other episodes, so it was kept"}
		}
	}
	return action{kind: actPlace, replace: conflicts}
}

func overlap(a, b []int64) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// subset reports whether every id in a is in b.
func subset(a, b []int64) bool {
	for _, x := range a {
		found := false
		for _, y := range b {
			if x == y {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// pruneDir removes a folder that an upgrade emptied, but never the library
// folder itself.
func pruneDir(libRoot, dir string) {
	if sameDir(dir, libRoot) || !pathmap.Within(libRoot, dir) {
		return
	}
	_ = os.Remove(dir) // only works while it is empty
}

// ---- Usenet clean-up ------------------------------------------------------------------

// cleanupSource tidies a Usenet download after its files were moved: the folder
// goes if it is clearly the download's own (inside the client's save folder),
// otherwise only empty folders are removed. A torrent is never touched: it is
// still seeding.
func (s *Service) cleanupSource(ctx context.Context, r *run) {
	if !r.srcIsDir || r.res.Outcome == OutcomeFailed {
		return
	}
	var save string
	if cp, err := s.deps.Downloads.ClientPaths(ctx, r.d.ClientID); err == nil {
		sp := r.d.SavePath
		if sp == "" {
			sp = cp.SavePath
		}
		save = pathmap.Map(cp.PathMap, sp)
	}
	inside := save != "" && !sameDir(save, r.srcPath) && pathmap.Within(save, r.srcPath)
	skippedAny := false
	for _, sk := range r.res.Skipped {
		if !sk.Existing {
			skippedAny = true
		}
	}
	if inside && !skippedAny {
		if err := os.RemoveAll(r.srcPath); err != nil {
			s.log.Warn("import: could not remove the finished Usenet download", "path", r.srcPath, "err", err)
		}
		return
	}
	removeEmptyDirs(r.srcPath)
}

// ---- concluding -------------------------------------------------------------------------

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (s *Service) conclude(ctx context.Context, r *run) {
	if r.res.Outcome == OutcomeFailed {
		s.fail(ctx, r)
	} else {
		s.succeed(ctx, r)
	}
}

type importDetail struct {
	Files     []Placed `json:"files"`
	Existing  []string `json:"alreadyThere,omitempty"`
	Skipped   []skip   `json:"skipped"`
	Blocked   bool     `json:"blocked,omitempty"`
	Removed   bool     `json:"removedFromClient,omitempty"`
	Searching bool     `json:"searchingAgain,omitempty"`
	Release   string   `json:"release"`
}

func (r *run) detail() importDetail {
	d := importDetail{Files: r.res.Imported, Skipped: r.res.Skipped, Blocked: r.res.Blocked, Removed: r.res.Removed,
		Searching: r.res.Searching, Release: r.d.ReleaseTitle}
	if d.Files == nil {
		d.Files = []Placed{}
	}
	if d.Skipped == nil {
		d.Skipped = []skip{}
	}
	for _, p := range r.adopted {
		d.Existing = append(d.Existing, p.dest)
	}
	return d
}

func (s *Service) succeed(ctx context.Context, r *run) {
	d := r.d
	titleID := d.TitleID

	// Tell the media servers first: what arrived, and what an upgrade took away.
	// The history then says which of them were told.
	var rescanned []string
	if s.deps.Servers != nil {
		paths := absPaths(r.folder.Path, r.placed, r.adopted)
		if len(paths) > 0 || len(r.removedAbs) > 0 {
			s.step(ctx, r, downloads.StepRescanning, "")
		}
		if len(paths) > 0 {
			rescanned = told(s.deps.Servers.Notify(ctx, mediaservers.Event{Kind: mediaservers.EventCreated, Paths: paths}))
		}
		if len(r.removedAbs) > 0 {
			if gone := told(s.deps.Servers.Notify(ctx, mediaservers.Event{Kind: mediaservers.EventDeleted, Paths: r.removedAbs})); len(paths) == 0 {
				rescanned = gone
			}
		}
	}

	withServers := func(detail map[string]any) map[string]any {
		if len(rescanned) > 0 {
			detail["rescanned"] = rescanned
		}
		return detail
	}
	if len(r.res.Imported) > 0 {
		var fresh []Placed
		for _, p := range r.res.Imported {
			if p.Upgrade {
				s.note(ctx, history.Entry{TitleID: titleID, CopyID: d.CopyID, DownloadID: d.ID, Event: history.Upgraded, Detail: withServers(map[string]any{
					"release": d.ReleaseTitle, "from": p.From, "to": p.Quality, "path": p.Path, "replaced": p.Replaced, "mode": p.Mode})})
				continue
			}
			fresh = append(fresh, p)
		}
		if len(fresh) > 0 {
			s.note(ctx, history.Entry{TitleID: titleID, CopyID: d.CopyID, DownloadID: d.ID, Event: history.Imported, Detail: withServers(map[string]any{
				"release": d.ReleaseTitle, "files": fresh, "skipped": len(r.res.Skipped)})})
		}
	} else if len(r.adopted) > 0 {
		s.note(ctx, history.Entry{TitleID: titleID, CopyID: d.CopyID, DownloadID: d.ID, Event: history.Imported, Detail: withServers(map[string]any{
			"release": d.ReleaseTitle, "files": []Placed{}, "alreadyThere": len(r.adopted)})})
	}

	if s.deps.Requests != nil && d.CopyID != 0 {
		if _, err := s.deps.Requests.SettleCopy(ctx, d.CopyID); err != nil {
			s.log.Warn("import: could not update the request", "copy", d.CopyID, "err", err)
		}
	}
	if s.hub != nil && (len(r.placed) > 0 || len(r.adopted) > 0 || len(r.removedAbs) > 0) {
		s.hub.Publish(LibraryTopic, map[string]any{
			"event": "imported", "titleId": titleID, "copyId": d.CopyID, "downloadId": d.ID, "files": len(r.placed) + len(r.adopted),
		})
	}
	s.afterImport(ctx, r)

	// Episodes the download was for that did not make it: let the search look
	// for them again instead of waiting out the day it keeps a grab in mind.
	if s.deps.Search != nil && r.det != nil && r.det.Kind == titles.KindShow && len(d.EpisodeIDs) > 0 && d.CopyID != 0 {
		var missing []int64
		for _, id := range d.EpisodeIDs {
			if !r.covered[id] {
				missing = append(missing, id)
			}
		}
		if keys := episodeKeys(r.det, missing); len(keys) > 0 {
			if err := s.deps.Search.ForgetGrab(ctx, d.CopyID, keys); err != nil {
				s.log.Warn("import: could not make the search forget some episodes", "err", err)
			}
		}
	}

	// Last, so "imported" (and the step "done") means everything above is done.
	if err := s.deps.Downloads.SetState(ctx, d.ID, downloads.StateImported, ""); err != nil {
		s.log.Warn("import: could not mark the download imported", "download", d.ID, "err", err)
	}
	if err := s.store.finish(ctx, r.res.ImportID, importEnd{
		Outcome: r.res.Outcome, Imported: len(r.res.Imported) + len(r.adopted), Skipped: len(r.res.Skipped),
		Reason: truncate(r.res.Reason, maxReasonRunes), Detail: r.detail(), At: s.now(),
	}); err != nil {
		s.log.Warn("import: could not record the result", "download", d.ID, "err", err)
	}
	s.log.Info("download imported", "download", d.ID, "title", titleID, "files", len(r.res.Imported), "outcome", r.res.Outcome)
}

// told lists the names of the media servers that took a notification.
func told(results []mediaservers.NotifyResult) []string {
	var out []string
	for _, res := range results {
		if res.OK && res.Server.Name != "" {
			out = append(out, res.Server.Name)
		}
	}
	return out
}

func absPaths(root string, groups ...[]plan) []string {
	var out []string
	for _, g := range groups {
		for _, p := range g {
			out = append(out, filepath.Join(root, filepath.FromSlash(p.dest)))
		}
	}
	return out
}

func (s *Service) note(ctx context.Context, e history.Entry) {
	if s.deps.History == nil || e.TitleID <= 0 {
		return
	}
	if err := s.deps.History.Record(ctx, e); err != nil {
		s.log.Warn("could not record history", "event", e.Event, "err", err)
	}
}

// fail records a failed import: the reason on the download, the blocklist for a
// bad release, the history, and another search.
func (s *Service) fail(ctx context.Context, r *run) {
	d := r.d
	reason := truncate(strings.TrimSpace(r.res.Reason), maxReasonRunes)
	errText := FailPrefix + reason
	r.res.Reason = reason

	if r.blame {
		if s.deps.Blocklist != nil && d.TitleID > 0 {
			_, err := s.deps.Blocklist.Block(ctx, decide.BlockEntry{
				TitleID: d.TitleID, GUID: d.GUID, InfoHash: blockHash(d), ReleaseTitle: d.ReleaseTitle, Reason: errText,
			})
			if err != nil {
				s.log.Warn("import: could not blocklist the release", "download", d.ID, "err", err)
			} else {
				r.res.Blocked = true
			}
		}
		if r.noMedia {
			if err := s.deps.Downloads.Remove(ctx, d.ID, true); err != nil {
				s.log.Warn("import: could not remove the bad download from its client", "download", d.ID, "err", err)
			} else {
				r.res.Removed = true
			}
		}
	}
	if err := s.deps.Downloads.SetState(ctx, d.ID, downloads.StateFailed, errText); err != nil {
		s.log.Warn("import: could not mark the download failed", "download", d.ID, "err", err)
	}

	if r.blame && s.deps.Search != nil && d.CopyID != 0 {
		var keys []string
		if r.det != nil {
			keys = grabKeys(r.det, d)
		}
		if len(keys) > 0 {
			if err := s.deps.Search.ForgetGrab(ctx, d.CopyID, keys); err != nil {
				s.log.Warn("import: could not make the search forget the bad release", "err", err)
			}
		}
		r.res.Searching = s.scheduleSearch(d.CopyID)
	}

	s.note(ctx, history.Entry{TitleID: d.TitleID, CopyID: d.CopyID, DownloadID: d.ID, Event: history.Failed, Detail: map[string]any{
		"release": d.ReleaseTitle, "reason": reason, "blocklisted": r.res.Blocked, "removedFromClient": r.res.Removed,
		"searchingAgain": r.res.Searching}})

	if err := s.store.finish(ctx, r.res.ImportID, importEnd{
		Outcome: OutcomeFailed, Imported: 0, Skipped: len(r.res.Skipped), Reason: reason, Retryable: r.res.Retryable,
		Detail: r.detail(), At: s.now(),
	}); err != nil {
		s.log.Warn("import: could not record the result", "download", d.ID, "err", err)
	}
	s.log.Warn("import failed", "download", d.ID, "title", d.TitleID, "reason", reason, "blocklisted", r.res.Blocked)
}

// blockHash is the info-hash to block: a torrent client's own id for a torrent
// is its info-hash.
func blockHash(d downloads.Download) string {
	if d.Protocol == grab.ProtocolTorrent && reHexHash.MatchString(d.ClientRef) {
		return strings.ToLower(d.ClientRef)
	}
	return ""
}

// grabKeys are the keys the search remembers a grab by, for what the download
// was for.
func grabKeys(det *titles.Detail, d downloads.Download) []string {
	switch det.Kind {
	case titles.KindMovie:
		return []string{search.MovieKey()}
	case titles.KindShow:
		if len(d.EpisodeIDs) > 0 {
			return episodeKeys(det, d.EpisodeIDs)
		}
		if rel := parse.ParseRelease(d.ReleaseTitle); rel.FullSeason && rel.Season > 0 {
			return []string{search.SeasonKey(rel.Season)}
		}
	case titles.KindArtist:
		// An artist's download carries the album it was for in its episode ids.
		var keys []string
		for _, id := range d.EpisodeIDs {
			keys = append(keys, search.AlbumKey(id))
		}
		return keys
	}
	return nil
}

func episodeKeys(det *titles.Detail, ids []int64) []string {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var keys []string
	for _, e := range det.Episodes {
		if want[e.ID] {
			keys = append(keys, search.EpisodeKey(e.Season, e.Number))
		}
	}
	return keys
}

// ---- searching again ----------------------------------------------------------------------

// scheduleSearch searches a copy again after a short wait. Failures that come
// close together share one search, and a copy is searched again this way at most
// a few times a day, so a run of bad releases cannot become a loop. It reports
// whether a search is now waiting.
func (s *Service) scheduleSearch(copyID int64) bool {
	if s.deps.Search == nil {
		return false
	}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if _, waiting := s.searchTimers[copyID]; waiting {
		return true
	}
	now := time.Now()
	recent := s.searchLog[copyID][:0:0]
	for _, t := range s.searchLog[copyID] {
		if now.Sub(t) < 24*time.Hour {
			recent = append(recent, t)
		}
	}
	if len(recent) >= maxSearchesPerDay {
		s.searchLog[copyID] = recent
		s.log.Warn("import: not searching again, this copy has failed too many imports today", "copy", copyID)
		return false
	}
	s.searchLog[copyID] = append(recent, now)
	s.searchTimers[copyID] = time.AfterFunc(s.searchDelay, func() {
		s.searchMu.Lock()
		delete(s.searchTimers, copyID)
		s.searchMu.Unlock()
		if s.baseCtx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(s.baseCtx, 10*time.Minute)
		defer cancel()
		if err := s.deps.Search.SearchCopy(ctx, copyID); err != nil && ctx.Err() == nil {
			s.log.Warn("import: the new search failed", "copy", copyID, "err", err)
		}
	})
	return true
}
