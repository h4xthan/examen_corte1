package transport

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"dvbs/internal/backup"
	"dvbs/internal/middleware"
	"dvbs/internal/model"
	"dvbs/internal/store"
)

// BackupHandler serves a logical dump of the whole database.
//
// It exists because TiDB Cloud Starter has no BACKUP statement: the feature is
// absent from the managed tier, not locked behind a privilege, so the only
// backup an application can take is the one it reads out itself. The panel is
// the only place it is reachable from, which is the only place a full copy of
// the database — every password hash, every reset token — belongs.
//
// Every dump that leaves the building is first written to the backup directory
// and then streamed as the download, so the machine has a copy of its own
// history even when the browser does not save the file.
type BackupHandler struct {
	dumper *backup.Dumper
	dir    string
	audit  store.Audit
	// now is the clock behind the filename, so a dump taken at 23:59:59 is
	// distinguishable from one taken a second later across a midnight.
	now func() time.Time
}

func NewBackupHandler(dumper *backup.Dumper, dir string, audits ...store.Audit) *BackupHandler {
	h := &BackupHandler{dumper: dumper, dir: dir, now: time.Now}
	if len(audits) > 0 {
		h.audit = audits[0]
	}
	return h
}

// logAudit records one row of backup history. The store is optional so tests
// that build the handler straight from a dumper keep working.
func (h *BackupHandler) logAudit(r *http.Request, action string, details string) {
	if h.audit == nil {
		return
	}
	entry := &model.AuditLog{
		Entity:  model.AuditEntityBackup,
		Action:  action,
		Details: details,
	}
	if id, ok := middleware.UserIDFrom(r.Context()); ok {
		entry.UserID = &id
	}
	if email := middleware.EmailFrom(r.Context()); email != "" {
		entry.UserEmail = &email
	}
	if err := h.audit.Record(r.Context(), entry); err != nil {
		slog.Warn("audit record failed", "entity", model.AuditEntityBackup, "action", action, "err", err)
	}
}

// Create dumps the database into the backup directory and streams the file as
// a download.
//
// POST, not GET. It is an operation that costs a full read of the database and
// produces a sensitive artefact, and putting it on GET would make it reachable
// by a prefetch, a link preview or a browser extension walking the page. POST
// also puts it behind the CSRF token the middleware already requires.
func (h *BackupHandler) Create(w http.ResponseWriter, r *http.Request) {
	adminID, _ := middleware.UserIDFrom(r.Context())
	started := time.Now()
	name := backup.Filename(h.now())

	// The operation is recorded before the dump reads, so the backup that goes
	// to disk carries its own history entry. A restore then matches the source
	// exactly: had this row landed after the read, the backup would always be
	// one operation short of the database it was taken from.
	h.logAudit(r, model.AuditActionCreate, name)

	// The dump lands in a temporary file first and is renamed only once it is
	// complete. Serving the download from the finished file means a failure
	// happens before the first byte reaches the browser, so a broken dump can
	// never be delivered wearing a 200 and a download header.
	tmp, err := os.CreateTemp(h.dir, name+".tmp-*")
	if err != nil {
		slog.Error("backup could not open the staging file", "admin_id", adminID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
			slog.Warn("backup staging file not removed", "name", tmpName, "err", err)
		}
	}()

	res, err := h.dumper.Dump(r.Context(), tmp)
	closeErr := tmp.Close()
	if err != nil {
		slog.Error("backup failed", "admin_id", adminID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if closeErr != nil {
		slog.Error("backup could not close the staging file", "admin_id", adminID, "err", closeErr)
		writeError(w, http.StatusInternalServerError, closeErr)
		return
	}
	if err := os.Rename(tmpName, filepath.Join(h.dir, name)); err != nil {
		slog.Error("backup could not move the staging file", "admin_id", adminID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// The headers go out first because the filename is decided here, and a
	// Content-Disposition set after the first write never reaches the browser.
	f, err := os.Open(filepath.Join(h.dir, name))
	if err != nil {
		slog.Error("backup file could not be opened for streaming", "admin_id", adminID, "name", name, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()
	h.ServeHeadersFor(w, name)
	if _, err := io.Copy(w, f); err != nil {
		slog.Error("backup file could not be streamed", "admin_id", adminID, "name", name, "err", err)
		return
	}

	// Every dump is recorded. Starter has no audit trail of its own, so this
	// log line is the only record that a full copy of the database — hashes and
	// tokens included — left the building.
	slog.Info("backup taken",
		"admin_id", adminID,
		"tables", res.Tables,
		"rows", res.Rows,
		"bytes", res.Bytes,
		"took", time.Since(started).Round(time.Millisecond).String(),
	)
}

// List is the backups interface's listing: the files in the backup directory,
// newest first, with their sizes and times.
func (h *BackupHandler) List(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	type row struct {
		Name    string    `json:"name"`
		Size    int64     `json:"size"`
		Created time.Time `json:"created"`
	}
	out := make([]row, 0, len(entries))
	filenameRe := regexp.MustCompile(`^dvbs-backup-\d{8}-\d{6}\.sql$`)
	for _, e := range entries {
		if e.IsDir() || !filenameRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, row{Name: e.Name(), Size: info.Size(), Created: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, http.StatusOK, out)
}

// Download serves one saved dump from the backups interface. The name is
// validated against the exact filename shape the handler itself produces, so
// no path element can climb out of the directory.
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !regexp.MustCompile(`^dvbs-backup-\d{8}-\d{6}\.sql$`).MatchString(name) {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	// ServeFile would follow symlinks and would answer with a directory listing
	// for anything that is not a file; the open below asks for a regular file
	// explicitly and happens before the headers commit the response.
	f, err := os.Open(filepath.Join(h.dir, name))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	defer f.Close()

	h.ServeHeadersFor(w, name)
	if _, err := io.Copy(w, f); err != nil {
		slog.Warn("backup file could not be streamed", "name", name, "err", err)
	}
}

// ServeHeadersFor sets the download headers. It is separate from Create so the
// order is visible: the content type and the filename have to be decided before
// the first write, and a Content-Disposition set afterwards never reaches the
// browser.
func (h *BackupHandler) ServeHeadersFor(w http.ResponseWriter, name string) {
	head := w.Header()
	head.Set("Content-Type", "application/sql; charset=utf-8")
	head.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// The dump has password hashes in it. A cached copy in a shared proxy or in
	// the browser's disk cache would outlive the admin who asked for it.
	head.Set("Cache-Control", "no-store")
	head.Set("X-Content-Type-Options", "nosniff")
}
