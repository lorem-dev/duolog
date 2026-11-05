# duolog built from this checkout, for Windows. It does what the POSIX script
# next to it does: build .cache\dev\duolog.exe only when missing,
# copy the skill on SessionStart, then run the build with the same arguments.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$bin = Join-Path $root '.cache\dev\duolog.exe'

if (-not (Test-Path $bin)) {
	New-Item -ItemType Directory -Force -Path (Split-Path $bin) | Out-Null
	# Parallel hooks may build at once: each writes its own file, then renames.
	$tmp = "$bin.$PID.exe"
	Push-Location $root
	try { & go build -o $tmp ./cmd/duolog 2>&1 | Write-Host } finally { Pop-Location }
	if ($LASTEXITCODE -eq 0) {
		Move-Item -Force $tmp $bin
	} else {
		Remove-Item -Force -ErrorAction SilentlyContinue $tmp
		if (Test-Path $bin) {
			[Console]::Error.WriteLine('duolog: the build failed; running the earlier build')
		} else {
			[Console]::Error.WriteLine("duolog: the build from $root failed and there is no earlier build")
			# Fall back to the build of the main checkout, else an installed duolog.
			$fallback = $null
			$common = (& git -C $root rev-parse --path-format=absolute --git-common-dir 2>$null)
			if ($LASTEXITCODE -eq 0 -and $common -and (Split-Path -Leaf $common) -eq '.git') {
				$cand = Join-Path (Split-Path -Parent $common) '.cache\dev\duolog.exe'
				if ($cand -ne $bin -and (Test-Path $cand)) {
					$fallback = $cand
					[Console]::Error.WriteLine("duolog: running the build of the main checkout, $cand")
				}
			}
			if (-not $fallback) {
				$here = $PSScriptRoot
				foreach ($c in (Get-Command duolog -All -CommandType Application -ErrorAction SilentlyContinue)) {
					if ((Split-Path -Parent $c.Source) -ne $here) {
						$fallback = $c.Source
						[Console]::Error.WriteLine("duolog: running the installed duolog, $fallback")
						break
					}
				}
			}
			if (-not $fallback) {
				# A worker without its gate must stop, as the installed hooks do.
				if ([Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }
				exit 1
			}
			$bin = $fallback
		}
	}
}

if ($args.Count -ge 2 -and $args[0] -eq 'hook' -and $args[1] -eq 'SessionStart' -and ($args -contains 'codex')) {
	$skill = Join-Path $root '.agents\skills\duolog'
	New-Item -ItemType Directory -Force -Path $skill | Out-Null
	Copy-Item -Force (Join-Path $root 'plugin\codex\skills\duolog\SKILL.md') (Join-Path $skill 'SKILL.md')
}

& $bin @args
exit $LASTEXITCODE
