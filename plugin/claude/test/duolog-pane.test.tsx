import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

// Nothing sits beneath the plugin: each hook here stands for the engine. A
// call on `$` is answered `{ value }`; an event by its own result.

const SURFACES = ['terminal', 'desktop'] as const

const SNAPSHOT = {
  version: 1,
  generated_at: '2026-01-01T00:00:00Z',
  line: 'duolog 1 peer 1 run',
  lead: { id: 'lead-1', agent: 'claude' },
  rex: { available: true },
  peers: [
    {
      id: 'p-1',
      role: 'reviewer',
      agent: 'codex',
      model: '',
      state: 'idle',
      working: false,
      task: 'review',
      tab: 't1',
      worktree: '.worktrees/p-1',
      last: null,
    },
  ],
  runs: [
    {
      id: 'd-1',
      role: 'coder',
      agent: 'claude',
      model: 'sonnet',
      state: 'running',
      started: null,
      ended: null,
      worktree: '.worktrees/d-1',
      task_dir: '.duolog/tasks/d-1',
      has_log: true,
      line: '',
    },
  ],
  asks_open: 0,
  load: { agents: [], queued: 0, window_5h: null, weekly: null },
}

const TAIL = {
  offset: 120,
  reset: false,
  lines: ['> reading the brief'],
  ended: false,
  state: 'running',
  cursor: 'c-1',
}

type Ran = { argv: readonly string[]; env: Record<string, string> | undefined }

type World = {
  exists?: boolean
  // The folders that hold .duolog/config.yaml; with it, exists is unused.
  projects?: string[]
  snapshot?: unknown
  exitCode?: number
  stderr?: string
  tail?: unknown
  tailExit?: number
  tailStderr?: string
  env?: Record<string, string>
}

const output = (exitCode: number, stdout: string, stderr = '') => ({
  value: { exitCode, stdout, stderr, isStdoutTruncated: false, isStderrTruncated: false },
})

// CONTROL matches what the mod must never draw: C0, DEL, C1 and the bidi
// overrides and isolates.
const CONTROL = /[\u0000-\u001f\u007f-\u009f‪-‮⁦-⁩]/

// stand answers the engine's part: the project check, the command and the
// pane, the session id, and every process the mod runs, which it records.
function stand(on: On, world: World = {}) {
  const ran: Ran[] = []
  const registered: string[] = []
  const opened: string[] = []
  const toasts: string[] = []
  const checked: string[] = []
  const clock = mock.clock(on)
  mock.env(on, world.env ?? {})
  on('fs.exists', (_, e) => {
    checked.push(e.path)
    if (world.projects === undefined) return { value: world.exists ?? true }

    return { value: world.projects.some(dir => e.path === `${dir}/.duolog/config.yaml`) }
  })
  on('command.register', (_, e) => {
    registered.push(e.name)

    return { value: { command: e.name } }
  })
  on('ui.open', (_, e) => {
    opened.push(e.id)

    return { value: { isPlaced: true } }
  })
  on('ui.panes', () => ({
    value: opened.map(id => ({ id, title: id, isShown: true, isFocused: false, isPlaced: true })),
  }))
  on('ui.toast', (_, e) => {
    toasts.push(e.text)

    return { value: undefined }
  })
  // The engine's own band above the prompt: plain text, no Button.
  on('ui.render', { component: 'AbovePrompt' }, ($, e) => {
    const { Text } = $.ui.resolve(e)

    return <Text>engine band</Text>
  })
  on('session.id', () => ({ value: 'lead-1' }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('prompt.submit', (_, e) => ({ text: e.text }))
  on('tool.call', () => ({ result: {} }))
  on('process.run', (_, e) => {
    ran.push({ argv: e.argv, env: e.init?.env })
    if (e.argv[2] === '--snapshot') {
      return output(world.exitCode ?? 0, JSON.stringify(world.snapshot ?? SNAPSHOT) + '\n', world.stderr)
    }
    if (e.argv[2] === 'tail') {
      return output(world.tailExit ?? 0, JSON.stringify(world.tail ?? TAIL) + '\n', world.tailStderr)
    }

    return output(0, 'Opened a tab for d-1\n')
  })

  return { ran, registered, opened, toasts, clock, checked }
}

const pane = <S extends (typeof SURFACES)[number]>(surface: S, bodyColumns = 100) =>
  ({
    plugin: 'duolog',
    surface,
    component: 'Pane',
    requestId: 'duolog',
    props: {
      title: 'duolog',
      isFocused: false,
      bodyColumns,
      placement: 'dock',
      scroll: { offset: 0, bodyRows: 20 },
      view: {},
    },
  }) as const

const band = <S extends (typeof SURFACES)[number]>(surface: S, hasSurvey = false) =>
  ({
    plugin: 'duolog',
    surface,
    component: 'AbovePrompt',
    props: { hasSurvey, isWorking: false, maxRows: 10, bodyColumns: 100, scroll: { offset: 0, bodyRows: 10 }, view: {} },
  }) as const

// bandLabel is the label of the Button the band draws, or undefined.
async function bandLabel(ui: { find: (q: { type: string }) => Promise<{ props: { label?: string } } | undefined> }) {
  return (await ui.find({ type: 'Button' }))?.props.label
}

const start = { cwd: '/work', surface: 'terminal', isInteractive: true } as const

const run = {
  command: 'duolog-pane',
  args: '',
  origin: { kind: 'composer' },
  presentation: { isFullscreen: true, columns: 200 },
} as const

for (const surface of SURFACES) {
  test(`no duolog project registers no command (${surface})`, async ($, on) => {
    const world = stand(on, { exists: false })
    await $.session.start({ ...start, surface })
    await world.clock.advance(5000)

    expect(world.registered).toEqual([])
    expect(world.ran).toEqual([])
  })

  test(`/duolog-pane opens the pane and returns no text (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    expect(world.registered).toEqual(['duolog-pane'])

    const answer = await $.command.run(run)

    expect(answer.text).toBeUndefined()
    expect(world.opened).toEqual(['duolog'])
  })

  test(`the pane lists peers and runs (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    expect(world.ran[0]?.argv).toEqual(['duolog', 'runs', '--snapshot', '--fast', '--lead', 'lead-1'])
    const ui = await $.ui.mount(pane(surface))
    expect(await ui.find({ type: 'Text', text: /p-1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /d-1/ })).toBeDefined()

    await ui.press({ key: 'watch:d-1' })
    expect(world.ran.at(-1)?.argv).toEqual(['duolog', 'watch', '--tab', 'd-1'])
    expect(world.toasts).toEqual(['Opened a tab for d-1'])
    await ui.unmount()
  })

  test(`a narrow pane switches between peers and runs (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface, 60))
    expect(await ui.find({ type: 'Text', text: /p-1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /d-1/ })).toBeUndefined()

    await ui.press({ key: 'list:runs' })
    expect(await ui.find({ type: 'Text', text: /d-1/ })).toBeDefined()
    expect(await ui.find({ type: 'Text', text: /p-1/ })).toBeUndefined()
    await ui.unmount()
  })

  test(`an unknown version shows nothing (${surface})`, async ($, on) => {
    const world = stand(on, { snapshot: { ...SNAPSHOT, version: 2 } })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    expect(await ui.find({ type: 'Text', text: /d-1/ })).toBeUndefined()
    await ui.unmount()
    const line = await $.ui.mount(band(surface))
    expect(await bandLabel(line)).toBeUndefined()
    await line.unmount()
  })

  test(`a failed poll shows nothing (${surface})`, async ($, on) => {
    const world = stand(on, { exitCode: 1 })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    expect(await ui.find({ type: 'Text', text: /d-1/ })).toBeUndefined()
    await ui.unmount()
    const line = await $.ui.mount(band(surface))
    expect(await bandLabel(line)).toBeUndefined()
    await line.unmount()
  })

  test(`the band above the prompt carries the line and opens the pane (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await world.clock.advance(3000)

    const line = await $.ui.mount(band(surface))
    expect(await bandLabel(line)).toBe('duolog 1 peer 1 run')
    expect(world.opened).toEqual([])
    await line.press({ key: 'duolog 1 peer 1 run' })
    expect(world.opened).toEqual(['duolog'])
    await line.unmount()
  })

  test(`a survey keeps the band (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await world.clock.advance(3000)

    const line = await $.ui.mount(band(surface, true))
    expect(await bandLabel(line)).toBeUndefined()
    await line.unmount()
  })

  test(`the tail call is marked for duolog (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    await ui.press({ key: 'log:d-1' })
    const tail = world.ran.at(-1)
    expect(tail?.argv).toEqual(['duolog', 'runs', 'tail', 'd-1', '--from', '-1', '--cursor', '', '--json'])
    expect(tail?.env?.DUOLOG_VIEWER).toBe('claude-mod')
    expect(await ui.find({ type: 'Text', text: /reading the brief/ })).toBeDefined()

    await world.clock.advance(1000)
    const next = world.ran.filter(one => one.argv[2] === 'tail').at(-1)
    expect(next?.argv).toEqual(['duolog', 'runs', 'tail', 'd-1', '--from', '120', '--cursor', 'c-1', '--json'])
    await ui.unmount()
  })

  test(`a quiet lead polls every 15 s until the next prompt (${surface})`, async ($, on) => {
    const world = stand(on, { snapshot: { ...SNAPSHOT, line: '', peers: [], runs: [] } })
    await $.session.start({ ...start, surface })
    await world.clock.advance(40000)
    const polls = world.ran.length
    await world.clock.advance(10000)
    expect(world.ran.length).toBeLessThanOrEqual(polls + 1)
    // A worker started from a terminal still shows within the slow rate.
    await world.clock.advance(20000)
    expect(world.ran.length).toBeGreaterThan(polls)

    const slow = world.ran.length
    await $.prompt.submit({ text: 'hello', origin: { kind: 'composer' }, wait: false })
    await world.clock.advance(6000)
    expect(world.ran.length).toBeGreaterThanOrEqual(slow + 2)
  })

  test(`a finished tool call starts polling again (${surface})`, async ($, on) => {
    const world = stand(on, { snapshot: { ...SNAPSHOT, line: '', peers: [], runs: [] } })
    await $.session.start({ ...start, surface })
    await world.clock.advance(40000)
    const polls = world.ran.length

    await $.tool.call({ tool: 'Bash', command: 'ls' })
    await world.clock.advance(3000)
    expect(world.ran.length).toBeGreaterThan(polls)
  })

  test(`a worker session registers nothing and never polls (${surface})`, async ($, on) => {
    const world = stand(on, { env: { DUOLOG_DEPTH: '1' } })
    await $.session.start({ ...start, surface })
    await world.clock.advance(5000)

    expect(world.registered).toEqual([])
    expect(world.ran).toEqual([])
  })

  test(`a failed log shows its error once and stops (${surface})`, async ($, on) => {
    const world = stand(on, {
      tailExit: 1,
      tailStderr: 'duolog: runs \u001b[31mtail\u001b[0m is refused‮ here\nsecond line\n',
    })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    await ui.press({ key: 'log:d-1' })
    const shown = await ui.find({ type: 'Text', text: /is refused/ })
    expect(shown?.text).toBe('duolog: runs [31mtail[0m is refused here')
    expect(shown?.props.dimColor).toBe(true)
    expect(await ui.find({ type: 'Text', text: /second line/ })).toBeUndefined()

    const tails = () => world.ran.filter(one => one.argv[2] === 'tail').length
    expect(tails()).toBe(1)
    await world.clock.advance(5000)
    expect(tails()).toBe(1)
    await ui.unmount()
  })

  test(`a failed poll shows its error (${surface})`, async ($, on) => {
    const world = stand(on, { exitCode: 1, stderr: 'duolog: no lead\u0007 here\n' })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    const shown = await ui.find({ type: 'Text', text: /no lead/ })
    expect(shown?.text).toBe('duolog: no lead here')
    expect(shown?.props.dimColor).toBe(true)
    await ui.unmount()
  })

  test(`control characters never reach the drawing (${surface})`, async ($, on) => {
    const dirty = '\u001b[2J\u0000\u007f\u0085‪‮⁦⁩'
    const world = stand(on, {
      snapshot: {
        ...SNAPSHOT,
        line: `duolog 1 peer${dirty} 1 run`,
        peers: [{ ...SNAPSHOT.peers[0], id: `p-1${dirty}`, role: `rev${dirty}`, state: `idle${dirty}` }],
        runs: [{ ...SNAPSHOT.runs[0], id: `d-1${dirty}`, role: `co${dirty}`, state: `running${dirty}` }],
      },
      tail: { ...TAIL, lines: [`> read${dirty}ing`] },
    })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    await ui.press({ key: `log:d-1[2J` })
    const texts = await ui.findAll({ type: 'Text' })
    expect(texts.length).toBeGreaterThan(0)
    for (const one of texts) expect(CONTROL.test(one.text)).toBe(false)
    expect(await ui.find({ type: 'Text', text: /> read\[2Jing/ })).toBeDefined()
    await ui.unmount()
    const line = await $.ui.mount(band(surface))
    expect(await bandLabel(line)).toBe('duolog 1 peer[2J 1 run')
    await line.unmount()
  })

  test(`a snapshot's error shows in the dim line (${surface})`, async ($, on) => {
    const world = stand(on, {
      snapshot: { ...SNAPSHOT, line: '', lead: null, peers: [], runs: [], error: 'lead: lead-1\u0007 is ‮not a live lead' },
    })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    const shown = await ui.find({ type: 'Text', text: /is not a live lead/ })
    expect(shown?.text).toBe('lead: lead-1 is not a live lead')
    expect(shown?.props.dimColor).toBe(true)
    await ui.unmount()
  })

  test(`a lead started in a subfolder of the project registers (${surface})`, async ($, on) => {
    const world = stand(on, { projects: ['/work'], env: { HOME: '/home/me' } })
    await $.session.start({ ...start, cwd: '/work/pkg/sub', surface })

    expect(world.registered).toEqual(['duolog-pane'])
    expect(world.checked).toEqual([
      '/work/pkg/sub/.duolog/config.yaml',
      '/work/pkg/.duolog/config.yaml',
      '/work/.duolog/config.yaml',
    ])
  })

  test(`the walk up stops below the home folder (${surface})`, async ($, on) => {
    const world = stand(on, { projects: ['/home/me'], env: { HOME: '/home/me' } })
    await $.session.start({ ...start, cwd: '/home/me/notes', surface })
    expect(world.registered).toEqual([])
    expect(world.checked).toEqual(['/home/me/notes/.duolog/config.yaml'])
  })

  test(`the walk up stops at home behind a /private link (${surface})`, async ($, on) => {
    const world = stand(on, { projects: ['/var/me'], env: { HOME: '/var/me' } })
    await $.session.start({ ...start, cwd: '/private/var/me/notes', surface })
    expect(world.registered).toEqual([])
    expect(world.checked).toEqual(['/private/var/me/notes/.duolog/config.yaml'])
  })

  test(`the walk up stops at home in another case (${surface})`, async ($, on) => {
    const world = stand(on, { projects: [], env: { USERPROFILE: 'c:\\users\\me' } })
    await $.session.start({ ...start, cwd: 'C:\\Users\\Me\\notes', surface })
    expect(world.registered).toEqual([])
    // The test host resolves a Windows path against its own folder, so only
    // the count and the end of the checked path are stable.
    expect(world.checked).toHaveLength(1)
    expect(world.checked[0]).toEndWith('C:\\Users\\Me\\notes/.duolog/config.yaml')
  })

  test(`a start folder above home is checked but not walked up from (${surface})`, async ($, on) => {
    const world = stand(on, { projects: [], env: { HOME: '/home/me' } })
    await $.session.start({ ...start, cwd: '/home', surface })
    expect(world.registered).toEqual([])
    expect(world.checked).toEqual(['/home/.duolog/config.yaml'])
  })

  test(`without a home folder only the start folder is checked (${surface})`, async ($, on) => {
    const world = stand(on, { projects: ['/work'], env: {} })
    await $.session.start({ ...start, cwd: '/work/pkg', surface })
    expect(world.registered).toEqual([])
    expect(world.checked).toEqual(['/work/pkg/.duolog/config.yaml'])
  })

  test(`the walk up outside home stops below the filesystem root (${surface})`, async ($, on) => {
    const world = stand(on, { projects: [], env: { HOME: '/home/me' } })
    await $.session.start({ ...start, cwd: '/srv/x', surface })
    expect(world.registered).toEqual([])
    expect(world.checked).toEqual(['/srv/x/.duolog/config.yaml', '/srv/.duolog/config.yaml'])
  })

  test(`each log line has a key (${surface})`, async ($, on) => {
    const world = stand(on, { tail: { ...TAIL, lines: ['one', 'two'] } })
    await $.session.start({ ...start, surface })
    await $.command.run(run)
    await world.clock.advance(1000)

    const ui = await $.ui.mount(pane(surface))
    await ui.press({ key: 'log:d-1' })
    expect((await ui.find({ key: 'log-line:0' }))?.text).toBe('one')
    expect((await ui.find({ key: 'log-line:1' }))?.text).toBe('two')
    await ui.unmount()
  })

  test(`a closed pane polls every 3 s (${surface})`, async ($, on) => {
    const world = stand(on)
    await $.session.start({ ...start, surface })
    await world.clock.advance(6000)
    expect(world.ran.length).toBe(2)
  })
}
