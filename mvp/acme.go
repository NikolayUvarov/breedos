package main

// v0.8.1 — ACME HTTP-01 challenge files.
//
// On the production host the reverse proxy routes every path of
// breedos.org to this binary, including /.well-known/acme-challenge/, so
// certbot's webroot challenge files were never reachable and the TLS
// certificate could not renew. The binary now serves challenge tokens from
// the host's webroot. With -acme-webroot=auto (the default) that is
// <binary dir>/../html, the layout the host uses
// (hosts/<domain>/engine/breedos next to hosts/<domain>/html); the route is
// only registered when <webroot>/.well-known/acme-challenge exists.

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const acmeChallengePrefix = "/.well-known/acme-challenge/"

// ACME tokens are base64url (RFC 8555 §8.1); anything else is refused, which
// also rules out path traversal.
var acmeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// resolveACMEDir returns the challenge directory for the -acme-webroot flag
// value, or "" when serving is disabled or the directory does not exist.
func resolveACMEDir(flagValue string) string {
	webroot := strings.TrimSpace(flagValue)
	switch webroot {
	case "", "off":
		return ""
	case "auto":
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		webroot = filepath.Join(filepath.Dir(exe), "..", "html")
	}
	dir := filepath.Join(webroot, ".well-known", "acme-challenge")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

func acmeChallengeHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "use GET", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, acmeChallengePrefix)
		if !acmeTokenPattern.MatchString(token) {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, token))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}
