package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestACMEChallengeServesTokenAndRefusesTraversal(t *testing.T) {
	webroot := t.TempDir()
	dir := filepath.Join(webroot, ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tok_EN-123"), []byte("tok_EN-123.thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webroot, "secret"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolveACMEDir(webroot)
	if got != dir {
		t.Fatalf("resolveACMEDir(%q) = %q, want %q", webroot, got, dir)
	}
	if resolveACMEDir("off") != "" || resolveACMEDir(filepath.Join(webroot, "missing")) != "" {
		t.Fatal("off or a missing webroot must disable serving")
	}
	h := acmeChallengeHandler(dir)
	cases := map[string]int{
		acmeChallengePrefix + "tok_EN-123":  http.StatusOK,
		acmeChallengePrefix + "absent":      http.StatusNotFound,
		acmeChallengePrefix + "../../secret": http.StatusNotFound,
		acmeChallengePrefix + "..%2fsecret":  http.StatusNotFound,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
		if want == http.StatusOK && rec.Body.String() != "tok_EN-123.thumb" {
			t.Errorf("%s: body %q", path, rec.Body.String())
		}
	}
}
