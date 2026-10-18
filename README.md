# Household finance

Self-hosted ledger for two people on a home LAN. Manual capture + CSV; bank sync is out of scope.

**Stack:** Go (`net/http`, `html/template`), Datastar, SQLite (`modernc.org/sqlite`), Chart.js, Docker Compose.

## Run locally

```bash
go run ./cmd/server
```

Open [http://127.0.0.1:3847](http://127.0.0.1:3847). On first visit, complete the setup wizard (household name, currency, two people).

Optional env:

| Variable | Default | Meaning |
| --- | --- | --- |
| `ADDR` | `:3847` | Listen address |
| `DATA_DIR` | `./data` | Data directory (backups land in `$DATA_DIR/backups`) |
| `DB_PATH` | `$DATA_DIR/finance.db` | SQLite path |
| `SECURE_COOKIE` | `false` | Set `true` behind HTTPS |

## LAN with Docker Compose

On the home machine (Linux or any Docker host on your Wi‑Fi):

```bash
git clone https://github.com/h0lyvltron/finance.home.git
cd finance.home
docker compose up -d --build
```

Then open `http://<host-lan-ip>:3847` from phones and laptops on the same network.

Find the LAN IP:

```bash
# Linux
hostname -I | awk '{print $1}'
# macOS
ipconfig getifaddr en0
```

SQLite lives in the named Docker volume `finance_data` (not on SMB/NFS). Restart policy is `unless-stopped`.

```bash
docker compose logs -f finance   # follow logs
docker compose down              # stop (volume kept)
```

**Firewall:** allow TCP `3847` from the LAN only if your host firewall blocks it. Do not publish the port to the public internet.

**PWA:** installable on the home screen over `http://` LAN IPs as a normal site; some install features that need public HTTPS may be limited.

**Away-from-home access** (Tailscale / WireGuard) is deferred — not required for this generation.

### Backup and restore

In the app: **More → Settings → Download backup** (`VACUUM INTO` snapshot).

Restore from CLI:

```bash
docker compose down
# Replace the DB inside the volume, e.g.:
docker run --rm -v finance_data:/data -v "$PWD":/backup alpine \
  cp /backup/finance-backup-YYYYMMDD-HHMMSS.db /data/finance.db
docker compose up -d
```

Local (non-Docker): stop the server, replace `$DB_PATH` with the backup file, start again.

## What works (slices 0–7)

- First-run setup, cookie sessions, bcrypt, CSRF
- Accounts (checking/savings/cash/credit/loan) with balance anchors + reconcile
- Categories, tags, payees; transactions, splits, transfers
- Soft-delete + 30‑minute undo toast + **recycle bin** (30‑day restore)
- Quick-add (phone sheet), offline localStorage queue → sync, durable **drafts**
- CSV import: mapping profiles, rules, preview, match/merge, review queue, batch undo, export
- Shared filters; Cash flow + Spending Explore (**vs last year**); pivot; net worth; budget vs actual; savings rate
- Plan: budgets, schedules/bills, left to spend; Goals; monthly recap (“money date”)
- Personal Home dashboards (widget catalog, reorder, S/M/L, range presets)
- Coaching: Home insights with snooze/dismiss + global **fewer nudges**
- Settings: month-start day (mid-month pay cycles), backup download

## Out of this generation

Bank aggregation, investments, multi-currency FX, receipt OCR, native apps, public OIDC, Tailscale/WireGuard, YNAB envelopes, OFX/QIF until needed, debt payoff planner, free-form dashboard grid, per-rule coaching settings page.
