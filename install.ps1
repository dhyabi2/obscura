# Obscura (OBX) installer / upgrader — Windows (PowerShell).
#
# Run it once to install + start a full node + miner. Run the SAME command again
# any time to upgrade: it notices a node is already running, checks the published
# release for a newer build, ASKS before doing anything, replaces the binary, and
# restarts it. Your wallet/miner keys live in %USERPROFILE%\.obscura and are NEVER
# touched by an upgrade — only the program binary is replaced.
#
#   iwr -useb https://obscura-blush.vercel.app/install.ps1 | iex
#
# Pass node flags via $NodeArgs (defaults: --mine --seeds <mainnet seed>):
#   & ([scriptblock]::Create((iwr -useb https://obscura-blush.vercel.app/install.ps1))) --mine --seeds 167.172.56.34:18080
#
# Env: OBX_DATADIR overrides the key/data directory (default %USERPROFILE%\.obscura).
param([Parameter(ValueFromRemainingArguments=$true)][string[]]$NodeArgs)
$ErrorActionPreference = 'Stop'

$Repo = 'dhyabi2/obscura'
$Tag  = 'v1.0.0'
$Base = "https://github.com/$Repo/releases/download/$Tag"
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$Asset = "Obscura-windows-$Arch.zip"
$Dir   = "Obscura-windows-$Arch"
$Bin   = Join-Path $Dir 'obscura-node.exe'
$DataDir = if ($env:OBX_DATADIR) { $env:OBX_DATADIR } else { Join-Path $env:USERPROFILE '.obscura' }
$Marker  = Join-Path $DataDir '.installed-sha'
$DefaultArgs = @('--mine', '--seeds', '167.172.56.34:18080')

function Get-PubSha {
  try {
    ((iwr -useb "$Base/SHA256SUMS.txt").Content -split "`n") |
      Where-Object { $_ -match [regex]::Escape($Asset) } |
      ForEach-Object { ($_ -split '\s+')[0] } | Select-Object -First 1
  } catch { $null }
}

$pub  = Get-PubSha
$proc = Get-Process obscura-node -ErrorAction SilentlyContinue | Select-Object -First 1
$have = if (Test-Path $Marker) { (Get-Content $Marker -Raw).Trim() } else { '' }

if ($proc) {
  Write-Host "Obscura node already running (pid $($proc.Id)), keys in $DataDir."
  if ($pub -and ($pub -eq $have)) {
    Write-Host "Already on the latest published build ($($pub.Substring(0,12))...). Nothing to do."
    return
  }
  Write-Host "A newer published build is available."
  if (-not [Environment]::UserInteractive) {
    Write-Host "  (no interactive console to confirm — re-run in an interactive PowerShell to upgrade)"
    return
  }
  $ans = Read-Host "Upgrade now? Your keys in $DataDir are preserved (only the binary is replaced) [y/N]"
  if ($ans -notmatch '^[yY]') { Write-Host "Upgrade skipped — the running node is untouched."; return }
  Write-Host "Stopping the running node (keys untouched)..."
  $proc | Stop-Process -Force
  Start-Sleep -Seconds 2
}

$tmp = Join-Path $env:TEMP $Asset
Write-Host "Downloading $Asset ..."
iwr -useb "$Base/$Asset" -OutFile $tmp
if ($pub) {
  $got = (Get-FileHash $tmp -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $pub.ToLower()) { throw "checksum mismatch (got $got, want $pub)" }
  Write-Host "Checksum verified ($($pub.Substring(0,12))...)."
}
Write-Host "Unpacking into $(Resolve-Path .)\$Dir ..."
if (Test-Path $Dir) { Remove-Item $Dir -Recurse -Force }
Expand-Archive -Path $tmp -DestinationPath . -Force

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
if ($pub) { Set-Content -Path $Marker -Value $pub }

$argv = if ($NodeArgs -and $NodeArgs.Count -gt 0) { $NodeArgs } else { $DefaultArgs }
Write-Host "Starting: $Bin $($argv -join ' ')"
Start-Process -FilePath (Resolve-Path $Bin) -ArgumentList $argv `
  -RedirectStandardOutput 'obscura.log' -RedirectStandardError 'obscura.err.log' -WindowStyle Hidden
Write-Host "Obscura node started. Logs: $(Resolve-Path .)\obscura.log"
