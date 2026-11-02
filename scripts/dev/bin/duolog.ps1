# duolog built from this checkout, for Windows. It does what the POSIX script
# next to it does: rebuild .cache\dev\duolog.exe when a source file is newer,
# copy the skill on SessionStart, then run the build with the same arguments.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$bin = Join-Path $root '.cache\dev\duolog.exe'

$stale = -not (Test-Path $bin)
if (-not $stale) {
	$built = (Get-Item $bin).LastWriteTimeUtc
	$sources = @('cmd', 'internal', 'plugin') | ForEach-Object { Get-ChildItem -Path (Join-Path $root $_) -Recurse -File }
	$sources += @('go.mod', 'go.sum', 'schema.go') | ForEach-Object { Get-Item (Join-Path $root $_) }
	$stale = [bool]($sources | Where-Object { $_.LastWriteTimeUtc -gt $built } | Select-Object -First 1)
}
if ($stale) {
	New-Item -ItemType Directory -Force -Path (Split-Path $bin) | Out-Null
	# Parallel hooks may build at once: each writes its own file, then renames.
	$tmp = "$bin.$PID.exe"
	Push-Location $root
	try { & go build -o $tmp ./cmd/duolog 2>&1 | Write-Host } finally { Pop-Location }
	if ($LASTEXITCODE -eq 0) {
		Move-Item -Force $tmp $bin
	} else {
		Remove-Item -Force -ErrorAction SilentlyContinue $tmp
		if (-not (Test-Path $bin)) {
			[Console]::Error.WriteLine("duolog: the build from $root failed and there is no earlier build")
			# A worker without its gate must stop, as the installed hooks do.
			if ([Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }
			exit 1
		}
		[Console]::Error.WriteLine('duolog: the build failed; running the earlier build')
	}
}

if ($args.Count -ge 2 -and $args[0] -eq 'hook' -and $args[1] -eq 'SessionStart' -and ($args -contains 'codex')) {
	$skill = Join-Path $root '.agents\skills\duolog'
	New-Item -ItemType Directory -Force -Path $skill | Out-Null
	Copy-Item -Force (Join-Path $root 'plugin\codex\skills\duolog\SKILL.md') (Join-Path $skill 'SKILL.md')
}

& $bin @args
exit $LASTEXITCODE
