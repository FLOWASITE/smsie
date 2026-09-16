# Spec: Persistent SIM operations

## Objective

Persist physical slot, subscriber number, Windows hardware path, and balance for each ICCID; expose safe admin APIs for profile updates and SQLite backup; prepare Windows service and private remote access without enabling billable schedules or a public tunnel by default.

## Stack and commands

- Go 1.25, Gin, GORM, SQLite, vanilla JavaScript.
- Test: `go test -tags nouac ./...`
- UI test: `node --test web/static/js/operations-console.test.js`
- Build: `go build -tags nouac -o smsie.exe .`

## API contract

- `PATCH /api/v1/modems/:iccid/profile`: admin-only partial update for `phone_number`, `hardware_path`, and `balance_vnd` (`balance_updated_at` is set automatically when `balance_vnd` is supplied). `slot_number` moved to `PATCH /api/v1/bays/:imei` (see `sim-slot-history.md`).
- `GET /api/v1/admin/backup`: admin-only consistent SQLite snapshot download.
- Restore remains disabled until Windows service restart semantics and upload validation are implemented and reviewed.

## Data rules

- ICCID identifies the SIM; slot number identifies the physical bay.
- Slot numbers are unique from 1 to 32 when present.
- Phone numbers contain 9-15 digits with an optional leading plus.
- Balance is a non-negative VND snapshot with timestamp, not inferred spend.
- Hardware path is stored separately from the mutable COM number.

## Security boundaries

- All write/backup endpoints require an authenticated admin.
- No database, backup, token, or password is committed to Git.
- Tunnel exposure is private-by-default; no quick public tunnel while the temporary admin password remains active.
- Automated calls/SMS and automatic top-up remain disabled.

## Acceptance criteria

- Khe 16 / 0924875662 / COM19 / ICCID / IMEI / hardware path survive browser and service restarts.
- Maintenance UI shows phone number, current balance, and last balance check.
- Backup downloads a valid SQLite snapshot without stopping the app.
- A checked-in Windows setup script can install the app as a service when run from an elevated terminal.
- Remote-access setup documents the exact remaining account/domain decision.
