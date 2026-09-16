#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$BackupPath
)

# Khôi phục qua staging: kiểm header SQLite, chép thành <db>.restore-pending rồi khởi động lại
# service. smsie.exe tự áp file pending lúc khởi động (kiểm integrity, chép bản cũ sang
# backups/pre-restore-*.db, đổi tên) — script này KHÔNG thay file DB trực tiếp.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$source = (Resolve-Path -LiteralPath $BackupPath).Path
$database = Join-Path $root 'smsie.db'
$pending = "$database.restore-pending"
$serviceName = 'smsie'

if ($source -eq $database) {
    throw 'BackupPath must not be the active smsie.db file'
}

$stream = [System.IO.File]::OpenRead($source)
try {
    $headerBytes = [byte[]]::new(16)
    if ($stream.Read($headerBytes, 0, 16) -ne 16) { throw 'Backup is too small' }
    $header = [System.Text.Encoding]::ASCII.GetString($headerBytes)
    if ($header -ne "SQLite format 3`0") { throw 'Selected file is not a SQLite database' }
}
finally {
    $stream.Dispose()
}

Copy-Item -LiteralPath $source -Destination $pending -Force

$service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($service) {
    Restart-Service -Name $serviceName
    Write-Output "Staged $pending and restarted $serviceName. Check logs/ for 'restore: đã áp'; rollback copy in backups/pre-restore-*.db"
} else {
    Write-Output "Staged $pending. Service $serviceName not installed: start smsie.exe manually to apply."
}
