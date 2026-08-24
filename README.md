# Gym tracker

A personal application for recording strength workouts at the gym, from a phone. The
requirements live in [CONTEXT.md](CONTEXT.md), deployment in [deploy/README.md](deploy/README.md).

One static binary: the frontend, the database migrations and TLS certificate issuance are
already inside it. The server needs no nginx, no certbot, no Python, no Node and no sqlite3.

The interface is in Russian — everything else, code and docs, is in English.

## How it is built

| Layer | Built with |
|---|---|
| Backend | Go, SQLite via `modernc.org/sqlite` (no cgo) |
| TLS | `autocert` in-process: Let's Encrypt certificates over tls-alpn-01 |
| Frontend | TypeScript + Preact + Vite, embedded into the binary via `go:embed` |
| Client-side storage | IndexedDB — the source of truth for rendering |
| Charts | hand-rolled inline SVG, no libraries |

**The application is offline-first.** The UI reads only from IndexedDB and never straight
from a network response; the server is the durable source of truth, and the network works as
a background reconciler. There is no separate "offline mode", because the code path is the
same either way.

Every action writes the new state and enqueues an operation in **one transaction**, after
which the queue drains in the background. The server applies a batch in one transaction too,
with an idempotence ledger and last-write-wins merging, so a retry, a reordering and a split
of the batch all produce the same result.

## Requirements

- Go 1.24+
- Node 20+

## Development

```bash
make dev                     # server on 127.0.0.1:8071
npm --prefix web run dev     # frontend on 5173, /api proxied to Go
```

The first user:

```bash
go run ./cmd/gymtracker adduser igor --admin --name=Igor
```

A program lives in `programs/<username>.json` — everyone has their own.

## Building and checks

```bash
make build     # binary for the current platform
make release   # binary for the server (Linux amd64)
make test      # Go and frontend tests
make e2e       # end-to-end browser scenario: offline, restart, catch-up sync
make check     # formatting, go vet, types
```

## What lives where

```
cmd/gymtracker/        entry point and subcommands: adduser, import, version
internal/
  db/                  two connection pools, pragmas, embedded migrations
  store/sync.go        the sync core: idempotence, LWW, conflict resolution
  store/changes.go     cursor-based delta selection
  confload/            what programs and guides share: id alphabet, strict decode, hashing
  program/             loading, validating and hashing programs
  guide/               loading, validating and hashing the exercise technique reference
  api/                 routes, authentication, brute-force limiting
  backup/              backups via VACUUM INTO with an integrity check
  server/              listeners: plain, and TLS with automatic certificates
  web/                 serving the embedded frontend
web/src/
  db/idb.ts            the atomic "state + queue" transaction
  db/merge.ts          merge rules, mirroring the server's
  sync/engine.ts       queue draining, backoff, save status
  ui/                  screens
testdata/merge_cases.json   truth table shared by Go and TypeScript
testdata/youtube_ids.json   the same, for the video id rule
programs/              training programs, one file per user
guides/exercises.json  the technique reference, one file for everyone
deploy/                systemd unit and deployment instructions
```

## The rule you must not break

`exercise_id` is forever. All history and every chart hang off it. Change an exercise and you
create a new id; never reuse a freed one, or a squat and a bench press will be glued into one
chart. History survives either way: every workout stores the hash of the program it was
recorded against and is rendered from that snapshot.
