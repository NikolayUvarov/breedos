package main

// v0.8.14 — private data room.
//
// Some conversations run on data that may not be published. /private asks
// for a login and password, then lists the CSV files in <binary dir>/
// private/data/ and lets them be loaded into the demo as the founder
// population ("private:<file>"). Nothing on these pages names a person:
// files are listed by name and size only.
//
// Layout next to the binary:
//
//	private/auth    one "user:sha256(user:password)" per line
//	private/secret  32 random bytes for signing sessions (created on first use)
//	private/data/   CSV tables (genotype, as for uploads)
//
// `breedos -private-hash user:password` prints a line for private/auth.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	privateCookie     = "breedos_private"
	privateSessionTTL = 12 * time.Hour
	privateLoginDelay = time.Second
	privateMaxFails   = 20 // per client address per hour
	privatePrefix     = "private:"
)

var privateFails = struct {
	sync.Mutex
	byAddr map[string][]time.Time
}{byAddr: map[string][]time.Time{}}

// privateDir is <binary dir>/private.
func privateDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "private"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Join(filepath.Dir(exe), "private")
}

// privateHash is the auth-file hash of a user and password.
func privateHash(user, password string) string {
	sum := sha256.Sum256([]byte(user + ":" + password))
	return hex.EncodeToString(sum[:])
}

// privateUsers reads private/auth; a missing file means no private area.
func privateUsers() map[string]string {
	raw, err := os.ReadFile(filepath.Join(privateDir(), "auth"))
	if err != nil {
		return nil
	}
	users := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if u, h, ok := strings.Cut(l, ":"); ok {
			users[strings.TrimSpace(u)] = strings.ToLower(strings.TrimSpace(h))
		}
	}
	return users
}

// privateSecret loads or creates the session-signing secret.
func privateSecret() ([]byte, error) {
	path := filepath.Join(privateDir(), "secret")
	if raw, err := os.ReadFile(path); err == nil && len(raw) >= 32 {
		return raw, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(privateDir(), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, err
	}
	return secret, nil
}

func privateSign(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// privateSession returns the logged-in user of a request, or "".
func privateSession(r *http.Request) string {
	c, err := r.Cookie(privateCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	parts := strings.Split(c.Value, "|")
	if len(parts) != 3 {
		return ""
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return ""
	}
	secret, err := privateSecret()
	if err != nil {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(privateSign(secret, parts[0]+"|"+parts[1])), []byte(parts[2])) != 1 {
		return ""
	}
	if _, ok := privateUsers()[parts[0]]; !ok {
		return ""
	}
	return parts[0]
}

func privateSetSession(w http.ResponseWriter, r *http.Request, user string) error {
	secret, err := privateSecret()
	if err != nil {
		return err
	}
	exp := strconv.FormatInt(time.Now().Add(privateSessionTTL).Unix(), 10)
	payload := user + "|" + exp
	http.SetCookie(w, &http.Cookie{Name: privateCookie, Value: payload + "|" + privateSign(secret, payload), Path: "/", HttpOnly: true, Secure: !loopbackHost(r.Host), SameSite: http.SameSiteLaxMode, MaxAge: int(privateSessionTTL.Seconds())})
	return nil
}

func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func clientAddr(r *http.Request) string {
	if fwd := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); fwd != "" {
		return fwd
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// privateTooManyFails records a failure and reports whether the address
// is over the hourly limit.
func privateTooManyFails(addr string, record bool) bool {
	privateFails.Lock()
	defer privateFails.Unlock()
	cutoff := time.Now().Add(-time.Hour)
	kept := privateFails.byAddr[addr][:0]
	for _, t := range privateFails.byAddr[addr] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if record {
		kept = append(kept, time.Now())
	}
	privateFails.byAddr[addr] = kept
	return len(kept) >= privateMaxFails
}

// privateDatasetFile maps "private:<name>" to a CSV under private/data,
// refusing anything that is not a plain file name.
func privateDatasetFile(name string) (string, bool) {
	if !strings.HasPrefix(name, privatePrefix) {
		return "", false
	}
	base := strings.TrimPrefix(name, privatePrefix)
	if base == "" || base != filepath.Base(base) || strings.HasPrefix(base, ".") || !strings.HasSuffix(strings.ToLower(base), ".csv") {
		return "", false
	}
	path := filepath.Join(privateDir(), "data", base)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return "", false
	}
	return path, true
}

// authorizePrivateDataset rejects a private dataset without a session.
func authorizePrivateDataset(r *http.Request, dataset string) error {
	if !strings.HasPrefix(dataset, privatePrefix) {
		return nil
	}
	if privateSession(r) == "" {
		return fmt.Errorf("dataset %q is private: log in at /private first", dataset)
	}
	if _, ok := privateDatasetFile(dataset); !ok {
		return fmt.Errorf("private dataset %q not found", dataset)
	}
	return nil
}

type privateDatasetInfo struct {
	Name        string `json:"name"`
	Bytes       int64  `json:"bytes"`
	Individuals int    `json:"individuals,omitempty"`
	Markers     int    `json:"markers,omitempty"`
	Loci        int    `json:"loci,omitempty"`
	Alleles     int    `json:"alleles,omitempty"`
	Ploidy      int    `json:"ploidy,omitempty"`
	Error       string `json:"error,omitempty"`
}

// privateDatasets lists private/data/*.csv with a parse summary.
func privateDatasets() []privateDatasetInfo {
	entries, err := os.ReadDir(filepath.Join(privateDir(), "data"))
	if err != nil {
		return nil
	}
	var out []privateDatasetInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".csv") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info := privateDatasetInfo{Name: e.Name()}
		if fi, err := e.Info(); err == nil {
			info.Bytes = fi.Size()
		}
		raw, err := os.ReadFile(filepath.Join(privateDir(), "data", e.Name()))
		if err != nil {
			info.Error = err.Error()
		} else if ds, err := parseDatasetCSV(raw, e.Name()); err != nil {
			info.Error = err.Error()
		} else {
			info.Individuals, info.Markers, info.Ploidy = len(ds.individuals), ds.markerCount, ds.ploidy
			if ds.ssr != nil {
				info.Loci, info.Alleles = ds.ssr.Loci, ds.ssr.Alleles
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// privateHandler serves /private (login or data room), /private/login,
// /private/logout and /private/data/<file>.
func privateHandler(w http.ResponseWriter, r *http.Request) {
	if privateUsers() == nil {
		http.Error(w, "no private area on this server", http.StatusNotFound)
		return
	}
	user := privateSession(r)
	switch {
	case r.URL.Path == "/private/login" && r.Method == http.MethodPost:
		addr := clientAddr(r)
		if privateTooManyFails(addr, false) {
			http.Error(w, "too many failed logins; try again later", http.StatusTooManyRequests)
			return
		}
		u, p := strings.TrimSpace(r.FormValue("user")), r.FormValue("password")
		want, ok := privateUsers()[u]
		if !ok || subtle.ConstantTimeCompare([]byte(want), []byte(privateHash(u, p))) != 1 {
			privateTooManyFails(addr, true)
			time.Sleep(privateLoginDelay)
			writePrivateLogin(w, "wrong login or password")
			return
		}
		if err := privateSetSession(w, r, u); err != nil {
			http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// No server redirect: the production proxy follows 3xx itself
		// and drops the Set-Cookie of the redirect response.
		writePrivateContinue(w, "Logged in.")
	case r.URL.Path == "/private/logout":
		http.SetCookie(w, &http.Cookie{Name: privateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writePrivateContinue(w, "Logged out.")
	case strings.HasPrefix(r.URL.Path, "/private/data/"):
		if user == "" {
			http.Error(w, "log in at /private first", http.StatusUnauthorized)
			return
		}
		path, ok := privateDatasetFile(privatePrefix + strings.TrimPrefix(r.URL.Path, "/private/data/"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, path)
	case r.URL.Path == "/private" || r.URL.Path == "/private/":
		if user == "" {
			writePrivateLogin(w, "")
			return
		}
		writePrivateRoom(w, user)
	default:
		http.NotFound(w, r)
	}
}

// privateDatasetsHandler: GET /api/private/datasets, the list for the demo.
func privateDatasetsHandler(w http.ResponseWriter, r *http.Request) {
	if privateSession(r) == "" {
		http.Error(w, "log in at /private first", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"datasets": privateDatasets()})
}

const privateStyle = `<style>body{font-family:system-ui,sans-serif;background:#0b1020;color:#e8ecf4;margin:0;padding:32px 16px;line-height:1.5}main{max-width:760px;margin:0 auto}h1{font-weight:600;font-size:22px}a{color:#8fffd1}table{border-collapse:collapse;width:100%;margin:16px 0}td,th{padding:8px 10px;border-bottom:1px solid #2a3350;text-align:left;font-size:14px}input{padding:8px 10px;border-radius:6px;border:1px solid #2a3350;background:#131a30;color:#e8ecf4;font-size:15px}button{padding:8px 16px;border-radius:6px;border:1px solid #8fffd1;background:transparent;color:#8fffd1;font-size:15px;cursor:pointer}.muted{color:#9aa4bd;font-size:13px}.err{color:#ff9a9a}</style>`

func writePrivateLogin(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	errLine := ""
	if msg != "" {
		errLine = `<p class="err">` + html.EscapeString(msg) + `</p>`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>BreedOS private</title>%s</head><body><main><h1>Private data room</h1><p class="muted">Tables here are not public and are shown only in person. Log in to continue.</p>%s<form method="post" action="/private/login"><p><input name="user" placeholder="login" autocomplete="username" required></p><p><input name="password" type="password" placeholder="password" autocomplete="current-password" required></p><p><button type="submit">Log in</button></p></form><p class="muted"><a href="/">BreedOS</a></p></main></body></html>`, privateStyle, errLine)
}

// writePrivateContinue is a 200 page that carries the cookie change and
// moves on to /private from the browser side.
func writePrivateContinue(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/private"><title>BreedOS private</title>%s</head><body><main><p>%s <a href="/private">Continue</a></p></main></body></html>`, privateStyle, html.EscapeString(msg))
}

func writePrivateRoom(w http.ResponseWriter, user string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var rows strings.Builder
	for _, d := range privateDatasets() {
		desc := d.Error
		if desc == "" {
			switch {
			case d.Loci > 0:
				desc = fmt.Sprintf("%d accessions × %d SSR loci, %d alleles", d.Individuals, d.Loci, d.Alleles)
			default:
				desc = fmt.Sprintf("%d accessions × %d markers", d.Individuals, d.Markers)
			}
			if d.Ploidy == 4 {
				desc += ", tetraploid"
			}
		}
		fmt.Fprintf(&rows, `<tr><td><code>%s</code></td><td>%s</td><td class="muted">%.0f KB</td><td><a href="/demo?private=%s">open in demo</a> · <a href="/private/data/%s">download</a></td></tr>`, html.EscapeString(d.Name), html.EscapeString(desc), float64(d.Bytes)/1024, html.EscapeString(d.Name), html.EscapeString(d.Name))
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="muted">no tables in private/data yet</td></tr>`)
	}
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>BreedOS private</title>%s</head><body><main><h1>Private data room</h1><p class="muted">Logged in as %s · <a href="/private/logout">log out</a>. These tables are not public: they are shown in person, not published, and nothing here names their source.</p><table><thead><tr><th>Table</th><th>Contents</th><th>Size</th><th></th></tr></thead><tbody>%s</tbody></table><p class="muted">In the demo the tables appear under Founder population as “Private: …”, for this browser session only. <a href="/demo">Open the demo</a> · <a href="/">BreedOS</a></p></main></body></html>`, privateStyle, html.EscapeString(user), rows.String())
}

// privateHashCommand implements `-private-hash user:password`.
func privateHashCommand(spec string) (string, error) {
	u, p, ok := strings.Cut(spec, ":")
	if !ok || strings.TrimSpace(u) == "" || p == "" {
		return "", fmt.Errorf("use -private-hash user:password")
	}
	return strings.TrimSpace(u) + ":" + privateHash(strings.TrimSpace(u), p), nil
}

// privateJSON is a small helper for tests.
func privateJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
