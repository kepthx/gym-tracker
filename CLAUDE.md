# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

An offline-first gym workout tracker: a Go server (SQLite, no cgo) that embeds a Preact/TypeScript
frontend into a single static binary. [CONTEXT.md](CONTEXT.md) is the product spec — its §2
"Mandatory properties" and §9 "What must not be in the application" are hard requirements, not
preferences. [README.md](README.md) covers the layout, [deploy/README.md](deploy/README.md) the
server setup.

## Commands

```bash
make build     # frontend + binary (frontend must be built first — see below)
make release   # linux/amd64 static binary into dist/
make test      # go test -race ./...  +  vitest
make e2e       # Playwright against the real binary (runs make build first)
make check     # gofmt -l . && go vet ./... && tsc --noEmit
make dev       # server on 127.0.0.1:8071
npm --prefix web run dev   # frontend on :5173, /api proxied to the Go process
```

Single tests:

```bash
go test ./internal/store -run TestApplyBatch -v
npm --prefix web run test -- src/sync/engine.test.ts
npm --prefix web run test -- -t 'name of the case'
npm --prefix web run e2e -- --grep 'offline'
```

**Every Go command fails on a fresh clone until `internal/web/dist` exists**, because
[embed.go](internal/web/embed.go) declares `//go:embed all:dist` and the directory is gitignored:
`pattern all:dist: no matching files found`. Run `make web` once, or `mkdir -p internal/web/dist &&
touch internal/web/dist/.gitkeep` if you only need the Go side. The `.gitkeep` that `.gitignore`
un-ignores is not actually committed.

There is no linter beyond `gofmt` and `go vet`; the frontend has no ESLint or Prettier — `tsc
--noEmit` is the whole check. Vite builds straight into `internal/web/dist`, so `make build` must
run `web` before `go build`.

`GYM_BASE` sets the path prefix the frontend is built for — `GYM_BASE=/gym/ make release` for a
deployment behind a reverse proxy that strips the prefix. It defaults to `/`. Nothing in the source
hardcodes a prefix: TypeScript derives it from `import.meta.env.BASE_URL`, `public/sw.js` derives it
from its own location (Vite's `base` does not rewrite `public/`), and `manifest.webmanifest` uses
URLs relative to itself so `scope` follows the app. A literal `scope` breaks iOS standalone launch
silently, which is why it must stay relative.

## Language convention

Code, comments and documentation are in English. **User-facing strings stay in Russian** — UI copy,
API error messages, Go `fmt.Errorf`/`errors.New` text, and `slog` attribute keys (`"пользователь"`,
`"ошибка"`). Match the surrounding file rather than translating either way.

## The sync protocol

This is the part of the codebase that needs reading across files. Everything hangs off three
properties, and any change to the write path has to preserve all three:

- **idempotent** — redelivering a batch changes nothing;
- **order-independent** — reorder or split the batch, get the same final state;
- **partially failable** — one bad op does not sink the other nine or jam the client queue.

The client writes state and enqueues the op in *one* IndexedDB transaction
([`applyLocal`](web/src/db/idb.ts)); the server applies a batch in one SQLite transaction with a
savepoint per op ([`ApplyBatch`](internal/store/sync.go)). A rejected op rolls back to its savepoint
and is reported as `rejected` in the per-op results — the HTTP status stays 200
([api/sync.go](internal/api/sync.go)). The client then moves it to a dead-letter store and drops it
from the outbox ([`settle`](web/src/sync/engine.ts)); a 401 does *not* clear the queue.

Mechanisms that make the above hold:

- `applied_ops` ledger, keyed by `op_id` UUID, is the second layer of idempotence on top of ops
  being idempotent by construction.
- Last-write-wins compares `(updated_ts, updated_by)` lexicographically — the device id breaks
  same-millisecond ties identically on every node ([`newer`](internal/store/merge.go)).
- Client timestamps are clamped to `[-7d, +1min]` around server time, so a phone with a broken
  clock cannot win every future comparison.
- `session.finish` and `session.delete` merge *monotonically* (smallest finish time wins; a
  tombstone is never undone), which is what makes them commutative. `session.start` and
  `set.upsert` use LWW.
- `set.upsert` always carries the **whole row**, never a patch.
- "Only one unfinished workout" is a partial unique index in the schema, not a code check. When two
  starts collide, the later-started one stays open and the other is closed at that start time —
  `startsLater` plus [`clampOverlappingFinishes`](internal/store/sync.go) make that outcome
  identical under either delivery order.
- Deltas are cursor-based on a single global `rev` counter shared by sessions and sets
  ([`NextRev`](internal/db/db.go), [changes.go](internal/store/changes.go)); the limit is applied at
  a common `rev` boundary so no row falls past the cursor.

### Merge rules are implemented twice

[internal/store/rowmerge.go](internal/store/rowmerge.go) and [web/src/db/merge.ts](web/src/db/merge.ts)
mirror each other, and both are driven by the same truth table,
[testdata/merge_cases.json](testdata/merge_cases.json). A divergence between them is the most
dangerous class of bug here. Changing a merge rule means editing three files together — Go, TS, and
the table.

### Adding an operation type

Touch all of: `OpType` + `validate` in [op.go](internal/store/op.go), a branch in `processOp` and
its `apply*` function in [sync.go](internal/store/sync.go), the `Op` type in
[web/src/types.ts](web/src/types.ts), and a creator in
[web/src/state/actions.ts](web/src/state/actions.ts). Ask whether the new op is LWW or monotone
before writing it.

## Client architecture

IndexedDB is the source of truth for rendering. The UI never reads a network response directly —
responses are merged into IndexedDB and the screen rereads from there. There is no "offline mode"
because there is only one code path. This matters because iOS restarts a home-screen app on nearly
every return, so a cold start must render from local data with no network.

- [state/store.ts](web/src/state/store.ts) holds a plain module-level state object with a listener
  set; `reloadFromStorage()` does a **full** reread of sessions/sets/programs after every write
  (a year is ~5k rows) rather than targeted updates.
- [state/selectors.ts](web/src/state/selectors.ts) derives everything — "last time", records,
  charts — client-side from local rows.
- [sync/engine.ts](web/src/sync/engine.ts) drains the outbox: 400 ms debounce, exponential backoff
  with jitter, 15 s poll while pending, reset on `visibilitychange`/`online`. Background Sync does
  not exist on iOS, so every trigger fires from the active page and the service worker holds no
  state.
- `SyncState` distinguishes `local` (queued, expected at the gym, shown amber and read as success)
  from `error` (needs attention). Do not collapse them — a queue with no signal must not look like
  a failure, or the save indicator stops being believed.
- Storage failing to open degrades visibly (`storageBroken`, `markDegraded`) instead of losing data
  silently; `openDB` races a 3 s timeout because some iOS builds hang forever.

## Programs and history

A program is `programs/<username>.json` — one file per user, changed by editing the file plus
`POST /api/admin/program/reload`, never by editing code. Programs are parsed, validated and hashed
into **immutable snapshots** addressed by the sha256 of their canonical form
([internal/program](internal/program/program.go)), so reformatting the file does not create a new
snapshot. A malformed program aborts startup.

Every session stores the `program_hash` it was recorded against, and history renders from *that*
snapshot rather than the current program. A client may legitimately start a workout offline against
a program that has since been replaced — `programAllowed` accepts any hash the user has already
trained by. The client caches snapshots by hash forever and sends `known_programs` so they are not
resent.

**`exercise_id` is forever.** All history and every chart hang off it. Changing an exercise means a
new id; never reuse a freed one. Likewise, a set's `idx` is assigned once and never renumbered —
deletion is `deleted = 1`, not a shift of its neighbours — because the composite key
`(session_id, exercise_id, idx)` is what makes LWW correct.

## Exercise guides

The technique reference lives in [guides/exercises.json](guides/exercises.json) — one file
for everyone, keyed by `exercise_id`, loaded by [internal/guide](internal/guide/guide.go) and
served at `GET /api/guides` with the hash of its canonical form as the ETag. `GYM_GUIDES`
points at it; `POST /api/admin/guides/reload` swaps it in without a restart. It never touches
the database.

It is **not** a field of the program on purpose. A program is canonicalised and hashed, every
session stores the `program_hash` it was recorded against, and history renders from that
snapshot — so prose inside the program would mint a new snapshot on every comma. Keying on
`exercise_id`, which is forever, is also what makes one guide correct for a workout recorded
against a program that has since been replaced. A missing guide and a guide for an exercise
no longer in the program are both normal; a malformed file stops startup, as a program does.

A **missing** file is where the two loaders deliberately differ. `guide.Load` treats it as
"this deployment ships no guides" and serves the empty set, which is right at startup.
`guide.Reload`, behind the admin endpoint, refuses — because a file that has gone missing is
almost always a rename or a typo in `GYM_GUIDES`, and reloading it as empty would hand every
client a new hash whose body is `{}`, wiping the reference off devices that are offline in a
basement. Use `Reload` for anything that runs after startup.

Both loaders go through [internal/confload](internal/confload/confload.go), which holds the
parts programs and guides genuinely share: the `^[a-z0-9_]{1,40}$` id alphabet (a guide key
*is* an exercise id, so the two files must never disagree about it), a strict decode that
rejects unknown fields **and** anything after the top-level object, and canonicalise-and-hash.
The trailing-content check is not pedantry: without it a merge conflict or a double paste
parses clean and boots a silently truncated file, which is the one outcome both formats exist
to prevent.

The demonstrations live in [media/](media/) and are served by
[serveMedia](internal/api/media.go) at **`/media/{name}`** — outside `/api/` on purpose, because
[public/sw.js](web/public/sw.js) is forbidden from caching `/api/` and these files must be
cached or the guide would need a connection every time. `GYM_MEDIA` points at the directory.

Two rules hold the media side together:

- **File names are derived from the exercise id, never stored**: `<id>.mp4` for a `clip`,
  `<id>-0.jpg` and `<id>-1.jpg` for `frames` ([`Media.Files`](internal/guide/guide.go)). Nothing
  in the guides file can name a path, which is why `mediaNameRe` in
  [media.go](internal/api/media.go) is the whole of the path defence.
- **A guide that promises a demonstration must have the files**, checked at load
  ([`missingMedia`](internal/guide/guide.go)). The guides file and the media directory are
  edited by hand, separately, and a card offering a clip that 404s reads as a broken app.

`credit` and `license` are required fields and are rendered under the demonstration: the clips
are CC BY 3.0 and attribution is a licence condition, not a nicety. Media is immutable by
convention — replacing a demonstration means a **new file name**, exactly like the hashed build
assets, because both the HTTP cache and the service worker treat it as immutable forever.

On the client the set lives in the `meta` store under `guides`/`guides_etag` rather than in a
store of its own, so there is no IndexedDB migration, and it is read before any network call
— the reference has to open in a basement gym.

## Server details worth knowing

- Two connection pools on one file ([db.go](internal/db/db.go)): the writer is capped at **one**
  connection and opened with `_txlock=immediate`, which is the single most important write-
  correctness setting — a deferred transaction that escalates its lock gets `SQLITE_BUSY` instantly
  regardless of `busy_timeout`. `synchronous=FULL`, and the pragmas are verified after opening.
- Migrations are embedded and run at startup, `NNNN_name.sql`, each in one transaction with its
  version bump.
- Auth: argon2id passwords, tokens stored only as sha256, 180-day TTL with sliding renewal (the
  password can never be asked for at the gym), per-IP login limiter. `GYM_DEBUG_AUTH=1` enables an
  `X-Debug-User` header stub — development only.
- CSRF is enforced via `Sec-Fetch-Site`/`Origin` in [middleware.go](internal/api/middleware.go); the
  CSP is strictly first-party, matching the "no third-party anything" rule in CONTEXT.md §9.
- Backups use `VACUUM INTO` from inside the process (nightly, plus opportunistically after a
  workout is finished if the last one is over 6 h old). `gymtracker import` restores an export —
  a round-trip test keeps the export honest.
- All configuration is `GYM_*` environment variables ([config.go](internal/config/config.go)).
  Setting `GYM_DOMAIN` switches on port 443 with in-process autocert; empty means plain HTTP.

## Tests

- [internal/store/sync_test.go](internal/store/sync_test.go) is the largest suite and the place to
  add cases for reordering, replay and conflict scenarios.
- Vitest runs in a `node` environment with `fake-indexeddb/auto` swapping in a real in-memory
  IndexedDB — the storage wrapper is exactly where atomicity lives, so it is not stubbed.
- Playwright boots the **actual binary** on a temp database via
  [web/e2e/server.mjs](web/e2e/server.mjs) and covers offline → restart → catch-up sync. It needs
  `make build` first (`make e2e` does that).
