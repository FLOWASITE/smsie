# Windows deployment

## smsie service

Build `smsie.exe`, keep `config.yaml`, `smsie.db`, and `web/` in the repository root, then run an elevated PowerShell terminal:

```powershell
.\deploy\windows\install-service.ps1
```

The script downloads the pinned official WinSW v2.12.0 x64 wrapper, installs `smsie` with automatic delayed start, and writes rolling logs under `logs/`.

## Backup and restore

Backups run automatically every day at `backup.hour` (default 03:00) into `backups/smsie-YYYYMMDD-HHMMSS.db`; each file is checked with `PRAGMA integrity_check` and only the newest `backup.keep` (default 14) are kept. **Báo cáo → Sao lưu** lists them, downloads any of them, and has **Sao lưu ngay**. **Sao lưu dữ liệu** still downloads a fresh snapshot to the browser.

Restore never replaces the live database while the app is running. Both paths stage the file as `smsie.db.restore-pending` and let `smsie.exe` apply it at the next start (integrity check → copy the current DB to `backups/pre-restore-*.db` → rename pending → live; audit `restore.applied`):

- **UI**: **Báo cáo → Khôi phục**, pick an existing backup or upload a `.db` (≤ 512 MB). Under the Windows service (`SMSIE_SERVICE=1`, set by `smsie-service.xml`) the app exits with code 3 after 2 s and WinSW restarts it; otherwise restart it by hand.
- **Script** (elevated terminal):

```powershell
.\deploy\windows\restore-backup.ps1 -BackupPath "C:\path\smsie-backup.db"
```

The script checks the SQLite header, copies the file to `smsie.db.restore-pending`, and runs `Restart-Service smsie`.

## Cloudflare Tunnel

Create a named tunnel and protect its hostname with Cloudflare Access before installing it. Then run:

```powershell
.\deploy\windows\install-cloudflare-tunnel.ps1 -TunnelToken "<token>"
```

Configure the tunnel origin as `http://localhost:18080`. Never commit or paste the token into this repository. Do not use a Quick Tunnel for production.
