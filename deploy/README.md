# Deployment

The application is a single static binary. The frontend, the database migrations and TLS
certificate issuance are all inside it already. **nginx, certbot, Python, Node and sqlite3
are not needed on the server.**

## 0. Prerequisites

A domain or subdomain whose A record points at the server's IP:

```bash
dig +short gym.example.com
# should return the server's IP
```

Until this command returns the right address, no certificate will be issued.

**Check that the ports are free** — the application takes them itself:

```bash
sudo ss -lntp | grep -E ':(80|443)\b'
# empty output means you can continue
```

If something already holds 443, this reverse-proxy-free arrangement will not work.

WireGuard runs over UDP and does not conflict, but make sure the firewall lets 80 and 443
through **without touching the tunnel's ports**.

## 1. Build

On your own machine:

```bash
make release
# dist/gymtracker-linux-amd64
```

Cross-compilation works without a toolchain: the SQLite driver is pure Go and cgo is unused.

## 2. User and directories on the server

```bash
sudo useradd --system --home /opt/gymtracker --shell /usr/sbin/nologin gymtracker
sudo mkdir -p /opt/gymtracker/programs
```

## 3. Files

```bash
scp dist/gymtracker-linux-amd64 server:/tmp/gymtracker
scp programs/*.json             server:/tmp/
scp deploy/gymtracker.service   server:/tmp/

# on the server
sudo install -m 755 -o gymtracker -g gymtracker /tmp/gymtracker /opt/gymtracker/gymtracker
sudo install -m 640 -o gymtracker -g gymtracker /tmp/*.json     /opt/gymtracker/programs/
sudo install -m 644 /tmp/gymtracker.service /etc/systemd/system/
```

## 4. Configuration

```bash
sudo tee /opt/gymtracker/.env > /dev/null << 'EOF'
GYM_DOMAIN=gym.example.com
GYM_ACME_EMAIL=you@example.com
# First deployment — against Let's Encrypt's staging directory.
GYM_ACME_STAGING=1
EOF

sudo chmod 600 /opt/gymtracker/.env
sudo chown gymtracker:gymtracker /opt/gymtracker/.env
```

There is no password in the configuration: it is set when the user is created, and only its
hash is stored.

## 5. Start

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gymtracker
sudo systemctl status gymtracker        # active (running)
sudo journalctl -u gymtracker -n 50 --no-pager
```

Check that the certificate was ordered (with the staging directory the browser will complain
about an untrusted certificate — that is expected):

```bash
curl -sk https://gym.example.com/healthz
# {"ok":true}
```

**Only after that** switch to production certificates:

```bash
sudo sed -i '/GYM_ACME_STAGING/d' /opt/gymtracker/.env
sudo rm -rf /var/lib/gymtracker/autocert     # the staging certificates are no longer needed
sudo systemctl restart gymtracker
```

The staging directory is not a formality here: Let's Encrypt allows **five identical
certificates per seven days**. A mistake in systemd, the firewall or DNS during a production
order blocks the domain for a week.

## 6. Application user

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker adduser igor --admin --name=Igor
# Пароль: ...
```

The program file's name has to match the username: `programs/igor.json`.
There is deliberately no open web registration — the application is personal.

## 7. Firewall

```bash
sudo ufw allow 80,443/tcp
sudo ufw status                          # confirm the WireGuard ports are still there
```

## 8. Onto the phone

Open `https://your-domain` in Safari → enter the password → Share → "Add to Home Screen".

**Inside the installed app the password will be asked for once more** — a home-screen app
has its own storage, separate from Safari's. After that no login will be needed for months:
the token's term is extended on every request.

---

## Backups

The process takes its own backups: at startup, then once a day, and additionally after a
finished workout if the previous backup is more than six hours old. Every backup is checked
for integrity, and a corrupt one is deleted. The 14 most recent are kept.

```
/var/lib/gymtracker/backups/db-20260728T040000Z.db
```

**A copy on the same disk is not yet a backup.** Pull it off the machine:

```bash
curl -sf -b cookies.txt https://your-domain/api/admin/backup -o backup.db
```

Plus the "Выгрузить всё (JSON)" button in the app — a complete dump of your own workouts.
The import subcommand takes the same file back:

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker import igor trenirovki-2026-07-28.json
```

Import merges data by the same rules as sync: it can be repeated, and it does not clobber
fresher records.

## Changing the training program

A program is defined by the file `programs/<username>.json` — everyone has their own.
No code change is needed:

```bash
sudo -u gymtracker vi /opt/gymtracker/programs/igor.json
sudo systemctl restart gymtracker
```

Or without a restart, as an admin:

```bash
curl -sf -b cookies.txt -X POST https://your-domain/api/admin/program/reload
```

A broken file is not accepted: at startup the service refuses to come up and names the rule
that was violated; on reload it returns 422 and leaves the previous program in place.

**The rule you must not break: `exercise_id` is forever.** Change an exercise and you create
a new id. Never reuse a freed id for a different exercise, or a squat and a bench press will
be glued into one chart. Data for exercises that dropped out of the program stays in the
database and in the export, and history keeps rendering from the snapshot of the program it
was recorded against.

## Updating

```bash
make release
scp dist/gymtracker-linux-amd64 server:/tmp/gymtracker
sudo install -m 755 -o gymtracker -g gymtracker /tmp/gymtracker /opt/gymtracker/gymtracker
sudo systemctl restart gymtracker
```

Database migrations are applied automatically at startup.

## A second user

The data model has been ready for this since day one; no migration will be needed:

```bash
sudo -u gymtracker GYM_DB=/var/lib/gymtracker/gymtracker.db \
  /opt/gymtracker/gymtracker adduser lena --name=Lena
sudo -u gymtracker vi /opt/gymtracker/programs/lena.json   # a program of their own
sudo systemctl restart gymtracker
```

Histories, programs and exports are fully separated. What is left is adding a name field to
the login screen — right now, while there is one user, it only has a password field.
