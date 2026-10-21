package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v0.8.14 — private data room.

// Point the private dir at a temp copy by running the binary path trick:
// privateDir derives from os.Executable, so tests create the layout
// next to the test binary.
func setupPrivateDir(t *testing.T) string {
	t.Helper()
	dir := privateDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	line, err := privateHashCommand("demo:secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth"), []byte("# users\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "toy.csv"), mustRead(t, "fixtures/upload-toy/genotype_ssr.csv"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPrivateLoginAndDatasets(t *testing.T) {
	setupPrivateDir(t)
	// No session: the room asks for a login, the API refuses, a private
	// dataset is not authorised.
	rec := httptest.NewRecorder()
	privateHandler(rec, httptest.NewRequest(http.MethodGet, "/private", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Log in") {
		t.Fatalf("login page expected: %d %s", rec.Code, rec.Body.String()[:80])
	}
	rec = httptest.NewRecorder()
	privateDatasetsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/private/datasets", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("datasets without a session: %d", rec.Code)
	}
	if err := authorizePrivateDataset(httptest.NewRequest(http.MethodPost, "/api/simulate", nil), "private:toy.csv"); err == nil {
		t.Fatal("private dataset without a session must be refused")
	}
	// Wrong password.
	form := strings.NewReader("user=demo&password=nope")
	req := httptest.NewRequest(http.MethodPost, "/private/login", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	privateHandler(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "wrong login") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("wrong password must not log in: %d", rec.Code)
	}
	// Right password: a session cookie, then the room, the list and the
	// dataset.
	req = httptest.NewRequest(http.MethodPost, "/private/login", strings.NewReader("user=demo&password=secret-pass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	privateHandler(rec, req)
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(cookies) != 1 || cookies[0].Name != privateCookie {
		t.Fatalf("login should redirect with a session cookie: %d %v", rec.Code, cookies)
	}
	withSession := func(method, path string) *http.Request {
		r := httptest.NewRequest(method, path, nil)
		r.AddCookie(cookies[0])
		return r
	}
	rec = httptest.NewRecorder()
	privateHandler(rec, withSession(http.MethodGet, "/private"))
	if !strings.Contains(rec.Body.String(), "toy.csv") || strings.Contains(rec.Body.String(), "Log in</button>") {
		t.Fatalf("data room expected after login: %s", rec.Body.String()[:200])
	}
	rec = httptest.NewRecorder()
	privateDatasetsHandler(rec, withSession(http.MethodGet, "/api/private/datasets"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"loci": 10`) {
		t.Fatalf("dataset list: %d %s", rec.Code, rec.Body.String())
	}
	if err := authorizePrivateDataset(withSession(http.MethodPost, "/api/simulate"), "private:toy.csv"); err != nil {
		t.Fatal(err)
	}
	if err := authorizePrivateDataset(withSession(http.MethodPost, "/api/simulate"), "private:../auth"); err == nil {
		t.Fatal("path escape must be refused")
	}
	ds, err := loadDataset("private:toy.csv")
	if err != nil || len(ds.individuals) != 48 {
		t.Fatalf("private dataset should load: %v", err)
	}
	// A tampered cookie is no session.
	bad := *cookies[0]
	bad.Value = strings.Replace(bad.Value, "demo|", "root|", 1)
	r := httptest.NewRequest(http.MethodGet, "/private", nil)
	r.AddCookie(&bad)
	if privateSession(r) != "" {
		t.Fatal("tampered cookie accepted")
	}
}
