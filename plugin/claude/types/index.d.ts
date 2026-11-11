// The state contract of the duolog pane mod (hooks/duolog-pane.tsx): the
// values it keeps in $.state under the plugin's name.

/** A live peer of the lead, as `duolog runs --snapshot` version 1 lists it. */
export type DuologPeer = {
  id: string
  role: string
  agent: string
  model: string
  state: string
}

/** A delegate run of the lead, as `duolog runs --snapshot` version 1 lists it. */
export type DuologRun = {
  id: string
  role: string
  agent: string
  model: string
  state: string
}

/** What the pane and the prompt hint draw from one snapshot. */
export type DuologSnapshot = {
  line: string
  peers: DuologPeer[]
  runs: DuologRun[]
}

/**
 * The log of one run shown under the lists: where the next page of
 * `duolog runs tail` starts, and the last lines read. A failed call ends
 * the log, with the first line of its error in `error`.
 */
export type DuologTail = {
  run: string
  offset: number
  cursor: string
  lines: string[]
  ended: boolean
  error: string
}

/** Which list the narrow pane shows. */
export type DuologList = 'peers' | 'runs'

declare module 'claude-code' {
  interface PluginState {
    duolog: {
      snapshot: Shaped<DuologSnapshot | null>
      tail: Shaped<DuologTail | null>
      list: DuologList
      /** Why the last snapshot failed, in one clean line; '' when it did not. */
      problem: string
    }
  }
}
