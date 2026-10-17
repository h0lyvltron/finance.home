# Household finance

Self-hosted, consumer-grade ledger for two people on a home LAN. Manual capture first; bank sync is out of scope.

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
| `DATA_DIR` | `./data` | Data directory |
| `DB_PATH` | `$DATA_DIR/finance.db` | SQLite path |
| `SECURE_COOKIE` | `false` | Set `true` behind HTTPS |

## LAN with Docker Compose

```bash
docker compose up -d --build
```

App: `http://<host-lan-ip>:3847`. SQLite lives in the `finance_data` volume.

## What works (slices 0–1)

- First-run setup, cookie sessions, bcrypt, CSRF
- Accounts (checking/savings/cash/credit/loan) with balance anchors
- Categories (starter pack), tags, payees
- Transactions, splits, transfers, soft-delete + undo toast
- Quick-add sheet, phone-friendly register, running balance
- Basic reconcile (statement date + ending balance)

## Deferred

Reports/dashboard builder, CSV import, budgets/coaching, PIN unlock, nightly backups UI — later slices per the product spec.
