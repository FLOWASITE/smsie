# Windows deployment

## smsie service

Build `smsie.exe`, keep `config.yaml`, `smsie.db`, and `web/` in the repository root, then run an elevated PowerShell terminal:

```powershell
.\deploy\windows\install-service.ps1
```

The script downloads the pinned official WinSW v2.12.0 x64 wrapper, installs `smsie` with automatic delayed start, and writes rolling logs under `logs/`.

## Backup and restore

Download a consistent snapshot from **Báo cáo → Sao lưu dữ liệu**. Restore only from an elevated terminal:

```powershell
.\deploy\windows\restore-backup.ps1 -BackupPath "C:\path\smsie-backup.db"
```

Restore stops the service and copies the current database to `backups/pre-restore-*.db` before replacing it.

## Cloudflare Tunnel

Create a named tunnel and protect its hostname with Cloudflare Access before installing it. Then run:

```powershell
.\deploy\windows\install-cloudflare-tunnel.ps1 -TunnelToken "<token>"
```

Configure the tunnel origin as `http://localhost:18080`. Never commit or paste the token into this repository. Do not use a Quick Tunnel for production.
