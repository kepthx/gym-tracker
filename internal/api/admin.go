package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// getExport returns a complete dump of the CURRENT user's data.
// It doubles as a backup and as material for analysing progress elsewhere.
func (a *API) getExport(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	now := time.Now()

	data, err := a.store.Export(r.Context(), uid, now)
	if err != nil {
		slog.Error("не удалось собрать выгрузку", "пользователь", uid, "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось собрать выгрузку")
		return
	}

	name := "trenirovki-" + now.Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		slog.Error("не удалось записать выгрузку", "ошибка", err)
	}
}

// getBackup serves the most recent backup file.
//
// A copy on the same disk is not yet a backup, so there has to be a way to pull it off the
// machine. Admin only: the file contains every user's data.
func (a *API) getBackup(w http.ResponseWriter, r *http.Request) {
	if a.backups == nil {
		writeError(w, http.StatusServiceUnavailable, "no_backups", "копии не настроены")
		return
	}
	path, err := a.backups.Latest()
	if err != nil {
		writeError(w, http.StatusNotFound, "no_backups", "копий пока нет")
		return
	}

	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "не удалось открыть копию")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "не удалось прочитать копию")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

type diagResponse struct {
	Version      string `json:"version"`
	Uptime       string `json:"uptime"`
	DBSize       int64  `json:"db_size"`
	WALSize      int64  `json:"wal_size"`
	LastBackupAt int64  `json:"last_backup_at"`
	Goroutines   int    `json:"goroutines"`
	ServerTime   int64  `json:"server_time"`
}

func (a *API) getDiag(w http.ResponseWriter, r *http.Request) {
	response := diagResponse{
		Version:    a.version,
		Uptime:     time.Since(a.startedAt).Truncate(time.Second).String(),
		Goroutines: runtime.NumGoroutine(),
		ServerTime: time.Now().UnixMilli(),
	}
	if info, err := os.Stat(a.dbPath); err == nil {
		response.DBSize = info.Size()
	}
	if info, err := os.Stat(a.dbPath + "-wal"); err == nil {
		response.WALSize = info.Size()
	}
	if a.backups != nil {
		if last := a.backups.LastRun(); !last.IsZero() {
			response.LastBackupAt = last.UnixMilli()
		}
	}
	writeJSON(w, http.StatusOK, response)
}

// postProgramReload rereads the programs directory from disk.
// Changing a program means editing a file, not editing code and not a rebuild.
func (a *API) postProgramReload(w http.ResponseWriter, r *http.Request) {
	if a.reloadPrograms == nil {
		writeError(w, http.StatusServiceUnavailable, "not_supported", "перезагрузка недоступна")
		return
	}
	attached, err := a.reloadPrograms(r.Context())
	if err != nil {
		// A broken program must not be accepted: handing someone a program other than the
		// one they train by is worse than refusing to reload.
		writeError(w, http.StatusUnprocessableEntity, "invalid_program", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attached": attached})
}
