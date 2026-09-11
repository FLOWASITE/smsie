#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$BackupPath
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$source = (Resolve-Path -LiteralPath $BackupPath).Path
$database = Join-Path $root 'smsie.db'
$backupDirectory = Join-Path $root 'backups'
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

New-Item -ItemType Directory -Path $backupDirectory -Force | Out-Null
$rollback = Join-Path $backupDirectory ("pre-restore-{0}.db" -f (Get-Date -Format 'yyyyMMdd-HHmmss'))

$service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($service -and $service.Status -ne 'Stopped') {
    Stop-Service -Name $serviceName
    $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
}
if (Test-Path -LiteralPath $database) {
    Copy-Item -LiteralPath $database -Destination $rollback
}
Copy-Item -LiteralPath $source -Destination $database -Force

if ($service) {
    Start-Service -Name $serviceName
}
Write-Output "Restore complete. Rollback copy: $rollback"
