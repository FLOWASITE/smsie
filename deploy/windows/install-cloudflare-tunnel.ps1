#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$TunnelToken
)

$ErrorActionPreference = 'Stop'
$cloudflared = 'C:\Program Files (x86)\cloudflared\cloudflared.exe'
if (-not (Test-Path -LiteralPath $cloudflared)) {
    throw 'cloudflared.exe is not installed at the expected path'
}
if ([string]::IsNullOrWhiteSpace($TunnelToken)) {
    throw 'TunnelToken is required'
}

& $cloudflared service install $TunnelToken
Get-Service -Name cloudflared | Select-Object Name, Status, StartType
