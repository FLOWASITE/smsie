#Requires -RunAsAdministrator
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\..')).Path
$wrapper = Join-Path $PSScriptRoot 'smsie-service.exe'
$config = Join-Path $PSScriptRoot 'smsie-service.xml'
$app = Join-Path $root 'smsie.exe'
$wrapperUrl = 'https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe'
$wrapperSha256 = '05B82D46AD331CC16BDC00DE5C6332C1EF818DF8CEEFCD49C726553209B3A0DA'

if (-not (Test-Path -LiteralPath $app)) {
    throw "smsie.exe not found at $app"
}
if (-not (Test-Path -LiteralPath (Join-Path $root 'config.yaml'))) {
    throw 'config.yaml must exist before installing the service'
}
if (-not (Test-Path -LiteralPath $wrapper)) {
    Invoke-WebRequest -Uri $wrapperUrl -OutFile $wrapper
}
if ((Get-FileHash -Algorithm SHA256 -LiteralPath $wrapper).Hash -ne $wrapperSha256) {
    throw 'WinSW checksum mismatch'
}

Get-CimInstance Win32_Process |
    Where-Object { $_.ExecutablePath -eq $app } |
    ForEach-Object { Stop-Process -Id $_.ProcessId }

$service = Get-Service -Name 'smsie' -ErrorAction SilentlyContinue
if ($service) {
    & $wrapper stop $config
    & $wrapper refresh $config
} else {
    & $wrapper install $config
}
& $wrapper start $config
& $wrapper status $config
