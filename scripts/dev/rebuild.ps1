# Verify a batch, replace the pinned build, then retire the lead's idle peers.
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$dir = Join-Path $root '.cache\dev'
$bin = Join-Path $dir 'duolog.exe'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$nonce = [Guid]::NewGuid().ToString('N')
$tmp = Join-Path $dir "rebuild.$PID.$nonce.exe"
$log = Join-Path $dir "rebuild.$PID.$nonce.log"

function Invoke-Checked {
    param([string]$Command, [string[]]$CommandArgs)
    # Windows PowerShell can treat redirected native stderr as an error record.
    $savedPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & $Command @CommandArgs > $log 2>&1
        $success = $?
        $code = $LASTEXITCODE
        if (-not $success) { if ($code -eq 0 -or $null -eq $code) { $code = 1 } }
    } finally {
        $ErrorActionPreference = $savedPreference
    }
    if ($code -ne 0) {
        Get-Content $log -Tail 20 | ForEach-Object { [Console]::Error.WriteLine([string]$_) }
        exit $code
    }
}

Push-Location $root
try {
    Invoke-Checked -Command 'go' -CommandArgs @('vet', './...')
    Invoke-Checked -Command 'go' -CommandArgs @('test', './...')
    Invoke-Checked -Command 'go' -CommandArgs @('build', '-o', $tmp, './cmd/duolog')
    $commit = & git rev-parse --short HEAD
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read the repository commit' }
    if (Test-Path $bin) {
        [System.IO.File]::Replace($tmp, $bin, [NullString]::Value)
    } else {
        [System.IO.File]::Move($tmp, $bin)
    }

    $closed = 0
    # Windows has no Rex peers. Outside Rex there is no session to control.
    if ($env:REX_SESSION -and $env:OS -ne 'Windows_NT') {
        $raw = & $bin peer list --json
        if ($LASTEXITCODE -ne 0) { throw 'Cannot list the lead''s peers' }
        $peers = $raw | ConvertFrom-Json
        foreach ($peer in $peers) {
            if ($peer.state -eq 'ready') {
                Invoke-Checked -Command $bin -CommandArgs @('peer', 'close', $peer.id)
                $closed++
            }
        }
    }
    Write-Output "rebuilt $commit; closed $closed idle peers"
} finally {
    Pop-Location
    Remove-Item -Force -ErrorAction SilentlyContinue $tmp, $log
}
