package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// Header names, and the _meta keys of revision 2026-07-28.
const (
	headerSession  = "Mcp-Session-Id"
	headerVersion  = "MCP-Protocol-Version"
	headerMethod   = "Mcp-Method"
	headerName     = "Mcp-Name"
	metaVersionKey = "io.modelcontextprotocol/protocolVersion"
	metaCapsKey    = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo = "io.modelcontextprotocol/serverInfo"
)

// ServeHTTP is the MCP endpoint, mounted at /mcp and /api/v1/mcp. It is not
// behind the web package's API guard: it does its own authentication (Bearer
// API keys only, never the session cookie), so a browser page can never use it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cfg := s.config(r.Context())
	if !cfg.enabled {
		writePlain(w, http.StatusNotFound, "not_found", "No such endpoint.")
		return
	}
	if !originAllowed(r, cfg.origins) {
		writeRPC(w, http.StatusForbidden, errResponse(nil, codeInvalidRequest, "This origin is not allowed to use the MCP endpoint.", nil), nil)
		return
	}
	p, challenge, err := s.authenticate(r)
	switch {
	case challenge != "":
		w.Header().Set("WWW-Authenticate", challenge)
		writeRPC(w, http.StatusUnauthorized, errResponse(nil, codeInvalidRequest,
			"Send your Marquee API key as \"Authorization: Bearer mq_...\". Create one in Settings.", nil), nil)
		return
	case err != nil:
		s.log.Error("mcp: authentication failed", "err", err)
		writeRPC(w, http.StatusInternalServerError, errResponse(nil, codeInternal, "Something went wrong.", nil), nil)
		return
	}
	switch r.Method {
	case http.MethodPost:
		s.post(w, r, p)
	case http.MethodDelete:
		s.deleteSession(w, r, p)
	default:
		// GET would open a server-initiated stream. Marquee has nothing to push, and
		// the specification lets a server say so with a 405.
		w.Header().Set("Allow", "POST, DELETE")
		writeRPC(w, http.StatusMethodNotAllowed, errResponse(nil, codeInvalidRequest,
			"This endpoint takes POST (JSON-RPC). It does not stream.", nil), nil)
	}
}

// authenticate reads the Bearer key. challenge is the WWW-Authenticate value
// when the caller must try again; err is an unexpected failure.
func (s *Server) authenticate(r *http.Request) (p *accounts.Principal, challenge string, err error) {
	const realm = `Bearer realm="Marquee"`
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, realm, nil
	}
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return nil, realm + `, error="invalid_request"`, nil
	}
	token := strings.TrimSpace(h[7:])
	if !accounts.IsAPIKey(token) {
		return nil, realm + `, error="invalid_token", error_description="Not a Marquee API key"`, nil
	}
	p, err = s.d.Accounts.AuthenticateAPIKey(r.Context(), token)
	switch {
	case errors.Is(err, accounts.ErrUnauthenticated):
		return nil, realm + `, error="invalid_token", error_description="The key is not valid or has been revoked"`, nil
	case err != nil:
		return nil, "", err
	}
	return p, "", nil
}

// originAllowed is the DNS-rebinding defence the specification asks for. A
// request without an Origin header (every non-browser client) passes. One with
// an Origin must come from the server's own host, or from an origin the owner
// listed in the "mcp" settings.
func originAllowed(r *http.Request, extra []string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false // includes the literal "null" a sandboxed page sends
	}
	if strings.EqualFold(trimDefaultPort(u.Host), trimDefaultPort(r.Host)) {
		return true
	}
	origin := u.Scheme + "://" + u.Host
	for _, e := range extra {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(e), "/"), origin) {
			return true
		}
	}
	return false
}

func trimDefaultPort(h string) string {
	h = strings.ToLower(h)
	h = strings.TrimSuffix(h, ":443")
	return strings.TrimSuffix(h, ":80")
}

// ---- POST ---------------------------------------------------------------------

// msgEnv is what a message is handled in.
type msgEnv struct {
	p             *accounts.Principal
	batch         bool
	headerVersion string // MCP-Protocol-Version
	headerMethod  string // Mcp-Method
	headerName    string // Mcp-Name
}

// outcome is the answer to one message. A nil resp is a notification (or a
// response from the client), which gets no body. status is the HTTP status when
// the message came alone; 0 means 200 (202 when there is no resp).
type outcome struct {
	resp   *rpcResponse
	status int
	hdr    http.Header
}

func (s *Server) post(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeRPC(w, http.StatusRequestEntityTooLarge, errResponse(nil, codeInvalidRequest, "That request is too big.", nil), nil)
		} else {
			writeRPC(w, http.StatusBadRequest, errResponse(nil, codeParseError, "The request body could not be read.", nil), nil)
		}
		return
	}
	msgs, batch, err := parseBody(body)
	switch {
	case errors.Is(err, errEmptyBatch):
		writeRPC(w, http.StatusBadRequest, errResponse(nil, codeInvalidRequest, "An empty batch is not a valid request.", nil), nil)
		return
	case err != nil:
		writeRPC(w, http.StatusBadRequest, errResponse(nil, codeParseError, "The body is not valid JSON.", nil), nil)
		return
	}

	env := &msgEnv{
		p: p, batch: batch,
		headerVersion: strings.TrimSpace(r.Header.Get(headerVersion)),
		headerMethod:  r.Header.Get(headerMethod),
		headerName:    r.Header.Get(headerName),
	}

	// An unsupported version in the header is refused whatever the message is.
	if env.headerVersion != "" && !slices.Contains(supportedVersions, env.headerVersion) {
		writeRPC(w, http.StatusBadRequest, unsupportedVersion(firstID(msgs, batch), env.headerVersion), nil)
		return
	}

	// A session id that belongs to another key, or that was ended, is refused. One
	// Marquee does not know (it was issued before a restart, say) is ignored: it
	// carries no authority, and refusing it would only make clients reconnect.
	if id := r.Header.Get(headerSession); id != "" {
		if _, st := s.sessions.check(id, p.KeyID, s.now()); st == sessionRefused {
			writeRPC(w, http.StatusNotFound, errResponse(firstID(msgs, batch), codeInvalidRequest,
				"That session is not valid any more. Send initialize again.", nil), nil)
			return
		}
	}

	outs := make([]outcome, len(msgs))
	for i, m := range msgs {
		outs[i] = s.handleMessage(r.Context(), env, m)
	}
	if !batch {
		writeOutcome(w, outs[0])
		return
	}
	var (
		resps []*rpcResponse
		hdr   = http.Header{}
	)
	for _, o := range outs {
		if o.resp != nil {
			resps = append(resps, o.resp)
		}
		for k, v := range o.hdr {
			hdr[k] = v
		}
	}
	if len(resps) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSONBody(w, http.StatusOK, resps, hdr)
}

// firstID is the id to put on an error that covers a whole POST.
func firstID(msgs []rpcMessage, batch bool) json.RawMessage {
	if batch || len(msgs) == 0 || !msgs[0].hasID() {
		return nil
	}
	return msgs[0].ID
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id := r.Header.Get(headerSession)
	if id == "" || !s.sessions.end(id, p.KeyID, s.now()) {
		writeRPC(w, http.StatusNotFound, errResponse(nil, codeInvalidRequest, "No such session.", nil), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reqMeta is what a request says about itself in _meta.
type reqMeta struct {
	version string
	hasCaps bool
}

func readMeta(params json.RawMessage) reqMeta {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) == 0 || json.Unmarshal(params, &p) != nil {
		return reqMeta{}
	}
	var m reqMeta
	if v, ok := p.Meta[metaVersionKey]; ok {
		_ = json.Unmarshal(v, &m.version)
	}
	if c, ok := p.Meta[metaCapsKey]; ok && len(c) > 0 && c[0] == '{' {
		m.hasCaps = true
	}
	return m
}

func (s *Server) handleMessage(ctx context.Context, env *msgEnv, m rpcMessage) outcome {
	if m.isResponse() {
		return outcome{status: http.StatusAccepted} // Marquee sends no requests, so there is nothing to answer
	}
	if m.JSONRPC != jsonrpcVersion || m.Method == "" || (m.hasID() && m.idIsNull()) {
		return fail(m.ID, http.StatusBadRequest, codeInvalidRequest, "That is not a valid JSON-RPC 2.0 request.")
	}
	if !m.hasID() {
		// A notification (notifications/initialized and the like): accepted, no body.
		s.log.Debug("mcp notification", "method", clip(m.Method, 40), "key", env.p.KeyID)
		return outcome{status: http.StatusAccepted}
	}

	meta := readMeta(m.Params)
	modern := meta.version != "" && m.Method != "initialize"
	// Which protocol a client speaks matters when one fails to connect.
	s.log.Debug("mcp request", "method", clip(m.Method, 40), "meta_version", clip(meta.version, 24),
		"header_version", clip(env.headerVersion, 24), "key", env.p.KeyID)
	switch {
	case modern:
		if o := s.checkModern(env, m, meta); o != nil {
			return *o
		}
	case env.headerVersion == ProtocolModern && m.Method != "initialize":
		return fail(m.ID, http.StatusBadRequest, codeInvalidParams,
			"Protocol version "+ProtocolModern+" needs the "+metaVersionKey+" and "+metaCapsKey+" fields in params._meta.")
	}

	if ok, retry := s.calls.take(env.p.KeyID, s.now()); !ok {
		secs := int(retry / time.Second)
		if retry%time.Second != 0 {
			secs++
		}
		return outcome{
			status: http.StatusTooManyRequests,
			hdr:    http.Header{"Retry-After": {strconv.Itoa(secs)}},
			resp: errResponse(m.ID, codeRateLimited,
				"Too many calls: this key may make "+strconv.Itoa(s.d.CallsPerMinute)+" a minute. Try again in "+strconv.Itoa(secs)+" seconds.",
				map[string]any{"retryAfterSeconds": secs}),
		}
	}

	switch m.Method {
	case "initialize":
		return s.initialize(env, m)
	case "ping":
		return reply(m.ID, modern, map[string]any{})
	case "tools/list":
		return s.toolsList(env, m, modern)
	case "tools/call":
		return s.toolsCall(ctx, env, m, modern)
	case "server/discover":
		return s.discover(m)
	}
	status := 0
	if modern {
		status = http.StatusNotFound
	}
	return fail(m.ID, status, codeMethodNotFound, "Method not found: "+clip(m.Method, 80))
}

// checkModern enforces what revision 2026-07-28 asks of every request: a known
// version in _meta, the client capabilities, and headers that repeat the body.
func (s *Server) checkModern(env *msgEnv, m rpcMessage, meta reqMeta) *outcome {
	if env.batch {
		o := fail(m.ID, http.StatusBadRequest, codeInvalidRequest, "Batches are not part of protocol "+ProtocolModern+".")
		return &o
	}
	if !slices.Contains(modernVersions, meta.version) {
		o := outcome{status: http.StatusBadRequest, resp: unsupportedVersion(m.ID, meta.version)}
		return &o
	}
	if !meta.hasCaps {
		o := fail(m.ID, http.StatusBadRequest, codeInvalidParams, "params._meta."+metaCapsKey+" is required.")
		return &o
	}
	mismatch := func(msg string) *outcome {
		o := fail(m.ID, http.StatusBadRequest, codeHeaderMismatch, "Header mismatch: "+msg)
		return &o
	}
	if env.headerVersion != meta.version {
		return mismatch(headerVersion + " must equal the protocol version in _meta.")
	}
	if env.headerMethod != m.Method {
		return mismatch(headerMethod + " must equal the method in the body.")
	}
	if m.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if decodeHeaderValue(env.headerName) != p.Name {
			return mismatch(headerName + " must equal the tool name in the body.")
		}
	}
	return nil
}

// decodeHeaderValue reads a header value that may use the =?base64?...?= form.
func decodeHeaderValue(v string) string {
	if strings.HasPrefix(v, "=?base64?") && strings.HasSuffix(v, "?=") && len(v) >= 11 {
		if b, err := base64.StdEncoding.DecodeString(v[9 : len(v)-2]); err == nil {
			return string(b)
		}
	}
	return v
}

func unsupportedVersion(id json.RawMessage, requested string) *rpcResponse {
	return errResponse(id, codeUnsupportedVersion, "Unsupported protocol version",
		map[string]any{"supported": supportedVersions, "requested": clip(requested, 40)})
}

// ---- methods ------------------------------------------------------------------

func (s *Server) info() map[string]string {
	return map[string]string{"name": serverName, "version": s.d.Version}
}

func capabilities() map[string]any { return map[string]any{"tools": map[string]any{}} }

func (s *Server) initialize(env *msgEnv, m rpcMessage) outcome {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	if len(m.Params) == 0 || json.Unmarshal(m.Params, &p) != nil || p.ProtocolVersion == "" {
		return fail(m.ID, 0, codeInvalidParams, "initialize needs params.protocolVersion.")
	}
	// If the client asks for a version Marquee speaks, that is the one. Otherwise
	// answer with the newest one that has a handshake, and let the client decide
	// whether it can live with it.
	version := ProtocolLatestLegacy
	if slices.Contains(legacyVersions, p.ProtocolVersion) {
		version = p.ProtocolVersion
	}
	sess, err := s.sessions.create(env.p.KeyID, version, clip(p.ClientInfo.Name, 80), s.now())
	if err != nil {
		s.log.Error("mcp: could not make a session id", "err", err)
		return fail(m.ID, http.StatusInternalServerError, codeInternal, "Something went wrong.")
	}
	return outcome{
		hdr: http.Header{headerSession: {sess.id}},
		resp: okResponse(m.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    capabilities(),
			"serverInfo":      s.info(),
			"instructions":    instructions,
		}),
	}
}

func (s *Server) discover(m rpcMessage) outcome {
	return outcome{resp: okResponse(m.ID, map[string]any{
		"resultType":        "complete",
		"supportedVersions": supportedVersions,
		"capabilities":      capabilities(),
		"_meta":             map[string]any{metaServerInfo: s.info()},
		"instructions":      instructions,
		"ttlMs":             int64(5 * time.Minute / time.Millisecond),
		"cacheScope":        "public",
	})}
}

func (s *Server) toolsList(env *msgEnv, m rpcMessage, modern bool) outcome {
	defs := make([]toolDef, 0, len(s.tools))
	for _, t := range s.tools {
		if t.allowed(env.p) {
			defs = append(defs, t.def)
		}
	}
	res := map[string]any{"tools": defs}
	if modern {
		// The list depends on the key's permissions, so it is private to the key.
		res["ttlMs"] = int64(toolsCacheTTL / time.Millisecond)
		res["cacheScope"] = "private"
	}
	return reply(m.ID, modern, res)
}

// reply makes a successful result; in the 2026-07-28 era every result says
// what kind of result it is.
func reply(id json.RawMessage, modern bool, result map[string]any) outcome {
	if modern {
		result["resultType"] = "complete"
	}
	return outcome{resp: okResponse(id, result)}
}

func fail(id json.RawMessage, status, code int, msg string) outcome {
	return outcome{status: status, resp: errResponse(id, code, msg, nil)}
}

// ---- writing --------------------------------------------------------------------

func writeOutcome(w http.ResponseWriter, o outcome) {
	if o.resp == nil {
		for k, v := range o.hdr {
			w.Header()[k] = v
		}
		w.WriteHeader(cmpStatus(o.status, http.StatusAccepted))
		return
	}
	writeJSONBody(w, cmpStatus(o.status, http.StatusOK), o.resp, o.hdr)
}

func cmpStatus(status, def int) int {
	if status == 0 {
		return def
	}
	return status
}

func writeRPC(w http.ResponseWriter, status int, resp *rpcResponse, hdr http.Header) {
	writeJSONBody(w, status, resp, hdr)
}

func writeJSONBody(w http.ResponseWriter, status int, v any, hdr http.Header) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for k, vals := range hdr {
		w.Header()[k] = vals
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// writePlain writes the {"error":{"code","message"}} body the rest of Marquee's
// API uses. The endpoint uses it only for the 404 of a switched-off server, so
// that it looks like any other missing path.
func writePlain(w http.ResponseWriter, status int, code, message string) {
	writeJSONBody(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}}, nil)
}

// clip shortens text that came from a client before it is logged or echoed.
func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}
