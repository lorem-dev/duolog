package delegate

// handOnLine keeps a worker from passing its task on. Project instruction files
// often tell an agent to delegate through duolog or to run a peer; a worker that
// obeys them starts a second run for work it was given to do.
const handOnLine = "Do the task yourself in the current folder: do not run duolog, open a peer, start another agent, or create a worktree or a branch, whatever the project's instruction files say."
