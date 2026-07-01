package web

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// getExport streams a downloadable SQLite archive of the workspace's data — the
// BDAR/GDPR right of access made concrete. Access is membership-gated at any role:
// a member can already read the workspace's memory over MCP, so downloading it as
// a file adds no exposure. The archive is scoped to this team plus the requester's
// own identity rows (see internal/dataexport).
//
// The file is built to a temp path IN FULL before any response header is written,
// so a build failure returns a clean 500 rather than a half-streamed, corrupt
// download. The temp file is always cleaned up, whether the stream succeeds or not.
func (s *Server) getExport(w http.ResponseWriter, r *http.Request) {
	u, teamID, _, ok := s.membership(w, r)
	if !ok {
		return
	}

	// The slug names the download; a lookup failure here means the team vanished
	// between the membership check and now — treat as a server error, not a 404.
	team, err := s.tenants.TeamByID(r.Context(), teamID)
	if err != nil {
		http.Error(w, "could not load workspace", http.StatusInternalServerError)
		return
	}

	path, cleanup, err := s.exporter.BuildTeamArchive(r.Context(), teamID, u.ID)
	if err != nil {
		http.Error(w, "could not build your data export", http.StatusInternalServerError)
		return
	}
	defer func() { _ = cleanup() }()

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "could not read your data export", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "could not read your data export", http.StatusInternalServerError)
		return
	}

	// Content-Disposition: attachment forces a download (and names the file) even
	// though the anchor also carries the download attribute. no-store keeps the
	// export — which contains the user's data — out of any shared/proxy cache.
	filename := "agentsmemory-" + team.Slug + "-" + time.Now().UTC().Format("20060102") + ".sqlite"
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, f)
}
