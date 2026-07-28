package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/kepthx/gym-tracker/internal/store"
)

// maxSyncBody caps the request body size. A batch of 500 operations fits into a few
// hundred kilobytes with a lot of room to spare.
const maxSyncBody = 4 << 20

type syncRequest struct {
	DeviceID      string     `json:"device_id"`
	Since         int64      `json:"since"`
	Limit         int        `json:"limit"`
	Ops           []store.Op `json:"ops"`
	KnownPrograms []string   `json:"known_programs"`
}

type syncResponse struct {
	Cursor     int64            `json:"cursor"`
	Results    []store.OpResult `json:"results"`
	Changes    store.ChangeSet  `json:"changes"`
	HasMore    bool             `json:"has_more"`
	ServerTime int64            `json:"server_time"`
}

// postSync is the core of the protocol: it accepts a batch of operations and returns the
// delta in the same response.
//
// A per-operation error never turns into an HTTP error: a batch of ten operations with one
// broken answers 200 and nine applied. Otherwise the client's queue would jam forever and
// everything behind the broken operation would be lost.
func (a *API) postSync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSyncBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "тело запроса слишком большое")
			return
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusUnprocessableEntity, "bad_request", "пустое тело запроса")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "не разбирается: "+err.Error())
		return
	}
	if req.DeviceID == "" {
		writeError(w, http.StatusUnprocessableEntity, "bad_request", "не задан device_id")
		return
	}

	uid := userID(r)
	now := time.Now()

	results, err := a.store.ApplyBatch(r.Context(), uid, req.DeviceID, req.Ops, now)
	if err != nil {
		if errors.Is(err, store.ErrBatchTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "batch_too_large", err.Error())
			return
		}
		// Database failure: the batch rolled back in full. The client will retry it — the
		// retry is safe thanks to the idempotence ledger.
		slog.Error("не удалось применить батч", "пользователь", uid, "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось сохранить")
		return
	}

	a.writeChanges(w, r, uid, req.Since, req.Limit, req.KnownPrograms, results, now)
}

func (a *API) getSync(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	since, _ := strconv.ParseInt(query.Get("since"), 10, 64)
	limit, _ := strconv.Atoi(query.Get("limit"))

	a.writeChanges(w, r, userID(r), since, limit, query["known_program"], nil, time.Now())
}

func (a *API) writeChanges(
	w http.ResponseWriter,
	r *http.Request,
	uid, since int64,
	limit int,
	knownPrograms []string,
	results []store.OpResult,
	now time.Time,
) {
	changes, err := a.store.Changes(r.Context(), uid, since, limit, knownPrograms)
	if err != nil {
		slog.Error("не удалось выбрать изменения", "пользователь", uid, "ошибка", err)
		writeError(w, http.StatusInternalServerError, "internal", "не удалось прочитать изменения")
		return
	}

	if results == nil {
		results = []store.OpResult{}
	}
	writeJSON(w, http.StatusOK, syncResponse{
		Cursor:  changes.Cursor,
		Results: results,
		Changes: changes.Changes,
		HasMore: changes.HasMore,
		// The client checks its clock against this and warns on a noticeable skew.
		ServerTime: now.UnixMilli(),
	})
}
