// The duolog pane for Claude Code: `/duolog-pane` opens a pane that lists
// the lead's peers and delegate runs, and the line under the prompt carries
// the worker count. It reads `duolog runs --snapshot`, version 1; a failed
// call or another version lists nothing and says why in one dim line. A
// worker session (DUOLOG_DEPTH 1 or more) registers nothing. The mod API is
// early access: on a Claude Code without it this module is never loaded, and
// duolog's hooks and status line work the same.
import { atom, read, update } from 'claude-code'
import type { ProcessRunResult, Register } from 'claude-code'

import type { DuologPeer, DuologRun, DuologSnapshot, DuologTail } from '../types'

const PANE = 'duolog'

// Change a tag when that value's fields change: it declines what an earlier
// version stored, which outlives a reload.
const snapshot = atom({ plugin: 'duolog', key: 'snapshot' } as const, null, { shape: 'snapshot-1' })
const tail = atom({ plugin: 'duolog', key: 'tail' } as const, null, { shape: 'tail-2' })
const list = atom({ plugin: 'duolog', key: 'list' } as const, 'peers')
const problem = atom({ plugin: 'duolog', key: 'problem' } as const, '')

// The timer ticks each second; a tick polls once this much time passed.
const TICK_MS = 1000
const OPEN_MS = 1000
const CLOSED_MS = 3000
// A lead with nothing to show this long stops the timer until the next
// prompt or tool call.
const QUIET_MS = 30000
const RUN_MS = 5000
// The pane keeps the last lines of a run's log.
const TAIL_LINES = 200
// The pane puts Peers and Runs side by side from this width.
const WIDE = 80

// CONTROL matches what no drawing may hold: C0, DEL, C1, and the bidi
// overrides and isolates, which could reorder or hide the text around them.
const CONTROL = /[\u0000-\u001f\u007f-\u009f‪-‮⁦-⁩]/g

// clean is s without control characters.
const clean = (s: string): string => s.replace(CONTROL, '')

// text is v as clean text, or '' when it is no string.
const text = (v: unknown): string => (typeof v === 'string' ? clean(v) : '')

// The longest error line the pane shows.
const PROBLEM_CHARS = 200

// firstLine is the first line of a command's stderr that holds text, clean
// and cut to PROBLEM_CHARS, or fallback.
function firstLine(stderr: string, fallback: string): string {
  for (const line of stderr.split(/\r?\n/)) {
    const shown = clean(line).trim()
    if (shown !== '') return shown.slice(0, PROBLEM_CHARS)
  }
  return fallback
}

// parseSnapshot reads one line of `duolog runs --snapshot`: version 1 only,
// else null. error is the snapshot's own error, such as a lead that is not
// live, clean and cut to PROBLEM_CHARS; '' when it has none.
function parseSnapshot(stdout: string): { snapshot: DuologSnapshot; error: string } | null {
  let doc: unknown
  try {
    doc = JSON.parse(stdout)
  } catch {
    return null
  }
  if (typeof doc !== 'object' || doc === null) return null
  const d = doc as Record<string, unknown>
  if (d.version !== 1 || !Array.isArray(d.peers) || !Array.isArray(d.runs)) return null
  const row = (v: unknown) => {
    const r = (typeof v === 'object' && v !== null ? v : {}) as Record<string, unknown>
    return { id: text(r.id), role: text(r.role), agent: text(r.agent), model: text(r.model), state: text(r.state) }
  }
  const peers: DuologPeer[] = d.peers.map(row).filter(p => p.id !== '')
  const runs: DuologRun[] = d.runs.map(row).filter(r => r.id !== '')
  return { snapshot: { line: text(d.line), peers, runs }, error: text(d.error).trim().slice(0, PROBLEM_CHARS) }
}

// The most folders the project lookup walks up.
const MAX_DEPTH = 64

// trimDir is dir without its trailing separators: '' for the root '/'.
function trimDir(dir: string): string {
  let d = dir
  while (/[\\/]$/.test(d)) d = d.slice(0, -1)
  return d
}

// parentDir is the folder above dir, or dir itself at a filesystem root:
// '/' or a drive such as 'C:'.
function parentDir(dir: string): string {
  const d = trimDir(dir)
  const i = Math.max(d.lastIndexOf('/'), d.lastIndexOf('\\'))
  if (d === '') return '/'
  if (i < 0) return d
  if (i === 0) return '/'
  return d.slice(0, i)
}

// sameDir reports whether a and b name the same folder, apart from
// trailing separators.
const sameDir = (a: string, b: string): boolean => trimDir(a) === trimDir(b)

// dirKey is dir in a form for comparing folders: forward slashes, lower
// case (macOS and Windows folders are case-insensitive) and without the
// macOS '/private' prefix, which '/var' and '/tmp' resolve to.
function dirKey(dir: string): string {
  const d = trimDir(dir).replace(/\\/g, '/').toLowerCase()
  return /^\/private(\/|$)/.test(d) ? d.slice('/private'.length) : d
}

// atOrAboveHome reports whether dir is the home folder or a folder above it.
// A false match only ends the project walk early.
function atOrAboveHome(dir: string, home: string): boolean {
  const d = dirKey(dir)
  const h = dirKey(home)
  return d === h || h.startsWith(`${d}/`)
}

// configIn is the path of .duolog/config.yaml in dir.
const configIn = (dir: string): string => `${trimDir(dir)}/.duolog/config.yaml`

type TailPage = { offset: number; reset: boolean; lines: string[]; ended: boolean; cursor: string }

// parseTail reads one page of `duolog runs tail --json`, or null.
function parseTail(stdout: string): TailPage | null {
  try {
    const p = JSON.parse(stdout) as Record<string, unknown>
    if (typeof p.offset !== 'number' || !Array.isArray(p.lines)) return null
    return {
      offset: p.offset,
      reset: p.reset === true,
      lines: p.lines.filter((l): l is string => typeof l === 'string').map(clean),
      ended: p.ended === true,
      cursor: text(p.cursor),
    }
  } catch {
    return null
  }
}

export const register: Register = on => {
  // The lead's id, and the call that starts the timer again: both set when
  // the session's project uses duolog. The engine is spelled `$` only inside
  // the hook that received it, so the timer's work lives in session.start.
  let lead = ''
  let start: (() => void) | undefined
  let timer: { cancel: () => void } | undefined
  // The poll guard: a tick polls only when no call is in flight.
  let inFlight = false
  let tailInFlight = false
  let lastPoll = -Infinity
  let quietSince: number | undefined

  // wake starts the timer again after a quiet spell.
  const wake = () => {
    if (timer === undefined) start?.()
  }

  // nextTail is the shown log after one call of `duolog runs tail` for run,
  // whose result is r (undefined when the call did not finish): one more
  // page, or the log ended with the call's error, so that a failing call is
  // shown once and never repeated each second. Another run's log stays.
  const nextTail = (cur: DuologTail | null, run: string, r: ProcessRunResult | undefined): DuologTail | null => {
    if (cur === null || cur.run !== run) return cur
    const fail = (error: string): DuologTail => ({ ...cur, ended: true, error })
    if (r === undefined) return fail('duolog runs tail did not answer')
    if (r.exitCode !== 0) return fail(firstLine(r.stderr, `duolog runs tail exited with ${r.exitCode}`))
    const page = parseTail(r.stdout)
    if (page === null) return fail('duolog runs tail sent a page this pane cannot read')
    const lines = [...(page.reset ? [] : cur.lines), ...page.lines].slice(-TAIL_LINES)
    return { run, offset: page.offset, cursor: page.cursor, lines, ended: page.ended, error: '' }
  }

  const tailArgv = (t: DuologTail) => [
    'duolog',
    'runs',
    'tail',
    t.run,
    '--from',
    String(t.offset),
    '--cursor',
    t.cursor,
    '--json',
  ]

  on('session.start', async ($, e, next) => {
    // A worker that duolog started is no lead: it shows nothing and polls
    // nothing.
    let depth = 0
    try {
      depth = Number.parseInt((await $.env.get('DUOLOG_DEPTH')) ?? '', 10)
    } catch {
      depth = 0
    }
    if (depth >= 1) return next(e)
    // The project is the start folder or a folder above it that holds
    // .duolog/config.yaml, as duolog finds a subfolder's project. The start
    // folder is always checked; the walk goes up only below the home folder,
    // and not at all when the home folder is unknown.
    let home = ''
    try {
      home = (await $.env.get('HOME')) || (await $.env.get('USERPROFILE')) || ''
    } catch {
      home = ''
    }
    let isProject = false
    for (let dir = e.cwd, i = 0; i < MAX_DEPTH; i++) {
      try {
        isProject = await $.fs.exists(configIn(dir))
      } catch {
        isProject = false
      }
      const up = parentDir(dir)
      if (isProject || home === '' || sameDir(up, dir) || atOrAboveHome(up, home)) break
      dir = up
    }
    if (!isProject) return next(e)
    await $.command.register({
      // A literal name, so validate reads the command.run hook below as the
      // one that answers its own command.
      name: 'duolog-pane',
      description: 'Show duolog peers and delegate runs in a pane',
      immediate: true,
    })
    lead = await $.session.id()

    const poll = async (now: number) => {
      let snap: DuologSnapshot | null = null
      let why = ''
      try {
        // Read each time: /clear gives the session a new id and runs no
        // session.start.
        lead = await $.session.id()
        const r = await $.process.run(['duolog', 'runs', '--snapshot', '--fast', '--lead', lead], {
          timeoutMs: RUN_MS,
        })
        if (r.exitCode !== 0) {
          why = firstLine(r.stderr, `duolog runs --snapshot exited with ${r.exitCode}`)
        } else {
          const parsed = parseSnapshot(r.stdout)
          if (parsed === null) {
            why = 'duolog sent a snapshot this pane cannot read'
          } else {
            snap = parsed.snapshot
            why = parsed.error
          }
        }
      } catch {
        snap = null
        why = 'duolog runs --snapshot did not answer'
      }
      await update($, snapshot, () => snap)
      await update($, problem, () => why)
      if (snap === null) await update($, tail, () => null)
      const quiet = snap === null || (snap.line === '' && !snap.runs.some(r => r.state === 'running'))
      if (!quiet) {
        quietSince = undefined
      } else if (quietSince === undefined) {
        quietSince = now
      } else if (now - quietSince >= QUIET_MS) {
        timer?.cancel()
        timer = undefined
      }
    }

    // pollTail reads the next page of the shown run's log.
    const pollTail = async () => {
      const t = await read($, tail)
      if (t === null || t.ended || tailInFlight) return
      tailInFlight = true
      try {
        let r: ProcessRunResult | undefined
        try {
          r = await $.process.run(tailArgv(t), { env: { DUOLOG_VIEWER: 'claude-mod' }, timeoutMs: RUN_MS })
        } catch {
          r = undefined
        }
        await update($, tail, cur => nextTail(cur, t.run, r))
      } finally {
        tailInFlight = false
      }
    }

    const tick = async () => {
      if (inFlight) return
      inFlight = true
      try {
        const now = await $.clock.now()
        let open = false
        try {
          open = (await $.ui.panes()).some(p => p.id === PANE)
        } catch {
          open = false
        }
        if (now - lastPoll >= (open ? OPEN_MS : CLOSED_MS)) {
          lastPoll = now
          await poll(now)
        }
        if (open) await pollTail()
      } catch {
        // The next tick tries again.
      } finally {
        inFlight = false
      }
    }

    timer?.cancel()
    timer = undefined
    start = () => {
      quietSince = undefined
      timer = $.clock.every(TICK_MS, () => void tick())
    }
    wake()

    return next(e)
  })

  // The answer has no text: a plugin's command text reaches the model.
  on('command.run', { command: 'duolog-pane' }, async $ => {
    await $.ui.open({ id: PANE, title: 'duolog' })
    lastPoll = -Infinity
    wake()

    return {}
  })

  // A prompt or a finished tool call starts the timer again. Neither hook
  // judges anything: one that fails passes the event on unchanged.
  on('prompt.submit', ($, e, next) => {
    wake()

    return next(e)
  }).catch(($, e, next) => next(e))

  on('tool.call', async ($, e, next) => {
    const result = await next(e)
    wake()

    return result
  }).catch(($, e, next) => next(e))

  // The band above the prompt holds the snapshot's line as a Button that
  // opens the pane, while the lead has workers. A survey keeps the band.
  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    if (e.props.hasSurvey) return next(e)
    const snap = await read($, snapshot)
    if (snap === null || snap.line === '') return next(e)
    const { Button } = $.ui.resolve(e)
    // Opens the pane and polls at once, as /duolog-pane does.
    const openPane = async () => {
      await $.ui.open({ id: PANE, title: 'duolog' })
      lastPoll = -Infinity
      wake()
    }

    return <Button label={snap.line} plain onPress={() => void openPane()} />
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const { Box, Button, Text } = $.ui.resolve(e)
    const snap = await read($, snapshot)
    const shown = await read($, tail)
    const which = await read($, list)
    const why = await read($, problem)
    const wide = e.props.bodyColumns >= WIDE

    // runButton runs a Button's command and shows its one line as a toast;
    // an error shows nothing.
    const runButton = async (argv: string[]) => {
      try {
        const r = await $.process.run(argv, { timeoutMs: 10000 })
        const first = r.exitCode === 0 ? firstLine(r.stdout, '') : ''
        if (first !== '') $.ui.toast(first)
      } catch {
        // Nothing to show.
      }
    }

    // toggleLog shows a run's log under the lists, its first page at once,
    // or hides the log shown.
    const toggleLog = async (run: string) => {
      if ((await read($, tail))?.run === run) {
        await update($, tail, () => null)
        return
      }
      const t: DuologTail = { run, offset: -1, cursor: '', lines: [], ended: false, error: '' }
      await update($, tail, () => t)
      if (tailInFlight) return
      tailInFlight = true
      try {
        let r: ProcessRunResult | undefined
        try {
          r = await $.process.run(tailArgv(t), { env: { DUOLOG_VIEWER: 'claude-mod' }, timeoutMs: RUN_MS })
        } catch {
          r = undefined
        }
        await update($, tail, cur => nextTail(cur, run, r))
      } finally {
        tailInFlight = false
      }
    }

    if (snap === null) {
      return (
        <Box flexDirection="column">
          <Text dimColor>{why === '' ? 'No duolog workers to show.' : why}</Text>
        </Box>
      )
    }

    const peers = (
      <Box flexDirection="column" flexGrow={1}>
        <Text bold>Peers</Text>
        {snap.peers.length === 0 && <Text dimColor>No live peers.</Text>}
        {snap.peers.map(p => (
          <Box key={`peer:${p.id}`} flexDirection="row" gap={1}>
            <Text>
              {p.id} {p.role} {p.agent} {p.state}
            </Text>
            <Button
              key={`view:${p.id}`}
              label="view"
              onPress={() => runButton(['duolog', 'peer', 'view', p.id, '--lead', lead])}
            />
          </Box>
        ))}
      </Box>
    )

    const runs = (
      <Box flexDirection="column" flexGrow={1}>
        <Text bold>Runs</Text>
        {snap.runs.length === 0 && <Text dimColor>No delegate runs.</Text>}
        {snap.runs.map(r => (
          <Box key={`run:${r.id}`} flexDirection="row" gap={1}>
            <Text>
              {r.id} {r.role} {r.model === '' ? r.agent : `${r.agent}/${r.model}`} {r.state}
            </Text>
            <Button
              key={`watch:${r.id}`}
              label="watch"
              onPress={() => runButton(['duolog', 'watch', '--tab', r.id])}
            />
            <Button
              key={`log:${r.id}`}
              label={shown?.run === r.id ? 'hide' : 'log'}
              onPress={() => toggleLog(r.id)}
            />
          </Box>
        ))}
      </Box>
    )

    const lists = wide ? (
      <Box flexDirection="row" gap={2}>
        {peers}
        {runs}
      </Box>
    ) : (
      <Box flexDirection="column">
        <Box flexDirection="row" gap={1}>
          <Button
            key="list:peers"
            label="Peers"
            variant={which === 'peers' ? 'primary' : 'secondary'}
            onPress={() => update($, list, () => 'peers')}
          />
          <Button
            key="list:runs"
            label="Runs"
            variant={which === 'runs' ? 'primary' : 'secondary'}
            onPress={() => update($, list, () => 'runs')}
          />
        </Box>
        {which === 'peers' ? peers : runs}
      </Box>
    )

    return (
      <Box flexDirection="column">
        {why !== '' && <Text dimColor>{why}</Text>}
        {lists}
        {shown !== null && (
          <Box flexDirection="column" marginTop={1}>
            <Text bold>Log of {shown.run}</Text>
            {/* A Text keeps no key, so a keyed Box holds each line. */}
            {shown.lines.map((line, i) => (
              <Box key={`log-line:${i}`}>
                <Text dimColor>{line}</Text>
              </Box>
            ))}
            {shown.error !== '' && <Text dimColor>{shown.error}</Text>}
          </Box>
        )}
      </Box>
    )
  })
}
