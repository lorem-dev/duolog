#!/bin/sh
# Runs a duolog binary on the system of a container, with no agents installed.
# Usage: smoke.sh /path/to/duolog
set -eu
bin=$1
export HOME=/tmp/smoke-home
mkdir -p "$HOME" /tmp/smoke/.claude/skills/foo
cd /tmp/smoke
printf -- '---\nname: foo\ndescription: Skill.\n---\nDo work.\n' > .claude/skills/foo/SKILL.md

"$bin" version
"$bin" install --scope project > /dev/null
test -s .codex/hooks.json
test -s .claude/settings.json
"$bin" sync --from claude > /dev/null
test -s .agents/skills/foo/SKILL.md
"$bin" doctor > /dev/null

# A worker's gate refuses when no lead is registered, on every system.
input='{"tool_name":"Bash","cwd":"/tmp/smoke","tool_input":{"command":"git status"}}'
answer=$(printf '%s' "$input" | DUOLOG_DEPTH=1 DUOLOG_ROLE=implement "$bin" gate --agent codex --event PreToolUse)
case $answer in
    *'"permissionDecision":"deny"'*) ;;
    *) echo "gate did not deny: $answer"; exit 1 ;;
esac

# The Codex hook guard, as installed, with the binary missing from PATH.
guard='if command -v duolog >/dev/null 2>&1; then exec duolog gate --agent codex --event PreToolUse; elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; fi'
grep -q 'elif \[ \\"${DUOLOG_DEPTH:-0}\\" -ge 1 \] 2>/dev/null; then exit 2; fi' .codex/hooks.json
code=0
DUOLOG_DEPTH=1 sh -c "$guard" < /dev/null || code=$?
test "$code" = 2
sh -c "$guard" < /dev/null

"$bin" uninstall --scope project
if grep -q 'duolog' .codex/hooks.json 2>/dev/null; then
    echo "uninstall left a hook"
    exit 1
fi
echo "ok: $(. /etc/os-release 2>/dev/null && echo "${PRETTY_NAME:-}")"
