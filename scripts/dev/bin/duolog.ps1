# duolog built from this checkout, for Windows. It does what the POSIX script
# next to it does: build .cache\dev\duolog.exe only when missing,
# copy the skill on SessionStart, then run the build with the same arguments.
#
# Windows PowerShell 5.1 turns every stderr line of a native command whose
# stderr is redirected into an error record, and 'Stop' makes that fatal. So
# native commands run with 'Continue' and their exit code decides.
$ErrorActionPreference = 'Stop'

# Inside a function $args is the function's own, so keep the script's here.
$launcherCommand = if ($args.Count -ge 1) { [string]$args[0] } else { '' }

# A worker's gate without its build must stop the tool, as the installed hooks
# do: exit 2. A hook ends with 0, because for a Stop hook 2 means "do not
# stop" and a peer would loop. Anything else fails with 1. An error the script
# does not handle ends the same way.
function Stop-Launcher {
	if ($script:launcherCommand -eq 'hook') { exit 0 }
	if ($script:launcherCommand -eq 'gate' -and [Environment]::GetEnvironmentVariable('DUOLOG_DEPTH') -match '^0*[1-9][0-9]*$') { exit 2 }
	exit 1
}
trap {
	[Console]::Error.WriteLine("duolog: $_")
	Stop-Launcher
}

# A native intermediary can pass pwsh's module path on to powershell.exe.
# Load this edition's built-ins explicitly before invoking any cmdlets.
if ($PSVersionTable.PSEdition -eq 'Desktop') {
	Import-Module ([IO.Path]::Combine($PSHOME, 'Modules\Microsoft.PowerShell.Utility'))
	Import-Module ([IO.Path]::Combine($PSHOME, 'Modules\Microsoft.PowerShell.Management'))
}

$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$bin = Join-Path $root '.cache\dev\duolog.exe'

if (-not (Test-Path $bin)) {
	New-Item -ItemType Directory -Force -Path (Split-Path $bin) | Out-Null
	# Parallel hooks may build at once: each writes its own file, then renames.
	$tmp = "$bin.$PID.exe"
	$built = $false
	Push-Location $root
	$ErrorActionPreference = 'Continue'
	try {
		# The build output goes to stderr, as in the POSIX launcher: the
		# hook's stdout is its answer to the agent.
		# A missing go is a failed build, as in the POSIX launcher: calling it
		# would throw, so look it up first and fall through to the fallback.
		if (Get-Command go -CommandType Application -ErrorAction SilentlyContinue) {
			& go build -o $tmp ./cmd/duolog 2>&1 | ForEach-Object { [Console]::Error.WriteLine([string]$_) }
			$built = ($LASTEXITCODE -eq 0) -and (Test-Path $tmp)
		} else {
			[Console]::Error.WriteLine('duolog: go is not on PATH, so this checkout was not built')
		}
	} finally {
		$ErrorActionPreference = 'Stop'
		Pop-Location
	}
	if ($built) {
		try {
			Move-Item -Force $tmp $bin
		} catch {
			# Another hook built at the same time and its build is running, so
			# Windows refuses to replace it. That build is as good as this one.
			if (-not (Test-Path $bin)) { throw }
			Remove-Item -Force -ErrorAction SilentlyContinue $tmp
		}
	} else {
		Remove-Item -Force -ErrorAction SilentlyContinue $tmp
		if (Test-Path $bin) {
			[Console]::Error.WriteLine('duolog: the build failed; running the earlier build')
		} else {
			[Console]::Error.WriteLine("duolog: the build from $root failed and there is no earlier build")
			# Fall back to the build of the main checkout, else an installed duolog.
			# DUOLOG_DEV_TRIED lists the launcher folders that already failed in
			# this chain of fallbacks; one of them never runs a fallback again.
			$here = $PSScriptRoot
			$tried = @(([Environment]::GetEnvironmentVariable('DUOLOG_DEV_TRIED') -split ';') | Where-Object { $_ })
			$fallback = $null
			$common = $null
			$gitOk = $false
			if ($tried -contains $here) {
				[Console]::Error.WriteLine('duolog: this launcher already ran in this chain of fallbacks, so it runs none again')
			} elseif (-not (Get-Command git -CommandType Application -ErrorAction SilentlyContinue)) {
				# Calling a missing git would throw past the installed fallback.
				[Console]::Error.WriteLine('duolog: git is not on PATH, so the main checkout was not looked for')
			} else {
				$ErrorActionPreference = 'Continue'
				try {
					$common = (& git -C $root rev-parse --path-format=absolute --git-common-dir 2>$null)
					$gitOk = $LASTEXITCODE -eq 0
				} finally {
					$ErrorActionPreference = 'Stop'
				}
			}
			if ($gitOk -and $common -is [string] -and (Split-Path -Leaf $common) -eq '.git') {
				$cand = Join-Path (Split-Path -Parent $common) '.cache\dev\duolog.exe'
				if ($cand -ne $bin -and (Test-Path $cand)) {
					$fallback = $cand
					[Console]::Error.WriteLine("duolog: running the build of the main checkout, $cand")
				}
			}
			if (-not $fallback -and -not ($tried -contains $here)) {
				foreach ($c in (Get-Command duolog -All -CommandType Application -ErrorAction SilentlyContinue)) {
					$dir = Split-Path -Parent $c.Source
					# Skip this launcher, another checkout's and any folder already tried.
					if ($dir -eq $here -or $dir -match '[\\/]scripts[\\/]dev[\\/]bin$' -or $tried -contains $dir) { continue }
					$fallback = $c.Source
					[Console]::Error.WriteLine("duolog: running the installed duolog, $fallback")
					break
				}
			}
			if (-not $fallback) { Stop-Launcher }
			$env:DUOLOG_DEV_TRIED = (@($tried) + $here) -join ';'
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
