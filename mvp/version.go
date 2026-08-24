package main

// v0.7.23 — Issue C. Build metadata exposed at /api/version.
//
// breedosVersion is updated alongside the other version-bump touchpoints
// (kicker, landing footers, CHANGELOG). breedosCommit and breedosBuildTime
// can be overridden at build time via:
//
//   go build -ldflags="-X main.breedosCommit=<sha> -X main.breedosBuildTime=<rfc3339>"
//
// When not overridden they fall back to "dev" / "unknown" so a local
// developer build still gives a sane response.

import (
	"encoding/json"
	"net/http"
)

var (
	breedosVersion   = "v0.7.39"
	breedosCommit    = "dev"
	breedosBuildTime = "unknown"
)

type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

func versionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(versionInfo{
		Version:   breedosVersion,
		Commit:    breedosCommit,
		BuildTime: breedosBuildTime,
	})
}
