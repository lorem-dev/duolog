package agent

import (
	"bytes"
	"errors"
	"strings"
)

// InstructionBegin and InstructionEnd are the lines that mark duolog's block in
// a project's instruction file. Install replaces everything between them, so a
// newer duolog updates the text; the rest of the file is the person's.
const (
	InstructionBegin = "<!-- duolog:begin -->"
	InstructionEnd   = "<!-- duolog:end -->"
)

// InstallOffer is the project instruction that tells an agent to offer the
// install on a machine without duolog. It is the first line of the block.
const InstallOffer = "If `.duolog/config.yaml` exists and `duolog` is not on PATH, offer the user this install, and run it only if they agree; macOS/Linux: `curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project`; Windows PowerShell: `irm https://github.com/lorem-dev/duolog/releases/latest/download/install.ps1 | iex` then add the printed directory to PATH and run `duolog install --scope project`."

// UsageNote is the project instruction that tells an agent to hand work to
// duolog. It follows InstallOffer in the block.
const UsageNote = "This project uses duolog. Hand work to Claude and Codex workers through duolog, as the duolog skill says: write the task to `.duolog/task.md`, open a peer in Rex (`duolog peer open <role>`, `peer send`, `peer wait`) when Rex is there, else `duolog delegate <role>`. Answer the peers' asks. Start a subagent only when duolog says `run here`."

// Block is the text that installation writes into each agent's project
// instruction file and doctor looks for: the markers and the lines between.
const Block = InstructionBegin + "\n" + InstallOffer + "\n" + UsageNote + "\n" + InstructionEnd + "\n"

// MCPNote is the block line for an agent whose lead gets the duolog MCP
// server. It follows UsageNote.
const MCPNote = "A Claude lead uses the duolog MCP tools (`delegate`, `peer`, `answer_ask`, `status`) for every hand-over, wait and ask (one waiting `delegate` call returns the whole result), and the CLI only where a tool is missing or fails; workers and peers never get these tools."

// BlockFor is the block for an agent: Block, with MCPNote when mcp is true.
func BlockFor(mcp bool) string {
	if !mcp {
		return Block
	}
	return InstructionBegin + "\n" + InstallOffer + "\n" + UsageNote + "\n" + MCPNote + "\n" + InstructionEnd + "\n"
}

// ServesMCP reports whether installer i is that of an agent whose lead gets
// the duolog MCP server, that is, whether it has a ServesMCP method that says
// so. Until the installer port names it, this keeps the check in one place.
func ServesMCP(i any) bool {
	s, ok := i.(interface{ ServesMCP() bool })
	return ok && s.ServesMCP()
}

// LegacyOfferMarker starts the one-line install offer of earlier versions.
// Install replaces such a line with the block, and uninstall removes it.
const LegacyOfferMarker = "<!-- duolog -->"

// ErrBlockMarkers says a begin or end marker has no partner, or they are out
// of order or repeated.
var ErrBlockMarkers = errors.New("duolog:begin and duolog:end markers do not pair up")

// FindBlock locates duolog's block in data: the byte range from the start of
// the begin line to the end of the end line, including its newline. found is
// false when the file has neither marker, and err is ErrBlockMarkers when it
// has only one, or more than one of either, or the end first.
func FindBlock(data []byte) (start, end int, found bool, err error) {
	begins, ends, offset := 0, 0, 0
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		switch strings.TrimRight(string(line), "\r\n") {
		case InstructionBegin:
			begins++
			start = offset
		case InstructionEnd:
			ends++
			end = offset + len(line)
		}
		offset += len(line)
	}
	switch {
	case begins == 0 && ends == 0:
		return 0, 0, false, nil
	case begins != 1 || ends != 1 || end <= start:
		return 0, 0, false, ErrBlockMarkers
	}
	return start, end, true, nil
}

// BlockCurrent reports whether data holds the block exactly as this version
// writes it, with LF and CRLF line endings taken as equal. present is false
// when it holds no block.
func BlockCurrent(data []byte) (current, present bool, err error) {
	return BlockCurrentFor(data, false)
}

// BlockCurrentFor is BlockCurrent against BlockFor(mcp).
func BlockCurrentFor(data []byte, mcp bool) (current, present bool, err error) {
	start, end, found, err := FindBlock(data)
	if err != nil || !found {
		return false, false, err
	}
	have := strings.ReplaceAll(string(data[start:end]), "\r\n", "\n")
	return strings.TrimRight(have, "\n") == strings.TrimRight(BlockFor(mcp), "\n"), true, nil
}
