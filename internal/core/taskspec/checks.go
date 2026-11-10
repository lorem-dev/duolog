package taskspec

import (
	"io/fs"
	"strings"
)

// CheckCommands returns the commands that the Check section of the task file
// name in fsys lists, and the lines it could not read as a command, as written.
// A command is a line, or a list item, that is one backtick span, with a
// leading "$ " cut off, or a line inside a fenced block. Prose, a span with
// text around it and an empty span are skipped, never guessed at.
func CheckCommands(fsys fs.FS, name string) (commands, skipped []string, err error) {
	return sectionCommands(fsys, name, "Check")
}

// RunCommands reads optional Run with the same command syntax as Check.
func RunCommands(fsys fs.FS, name string) (commands, skipped []string, err error) {
	return sectionCommands(fsys, name, "Run")
}

func sectionCommands(fsys fs.FS, name, section string) (commands, skipped []string, err error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, nil, err
	}
	parts, err := parse(data)
	if err != nil {
		return nil, nil, err
	}
	var f checkScan
	for _, line := range strings.Split(parts[section], "\n") {
		f.add(strings.TrimSpace(line))
	}
	return f.commands, f.skipped, nil
}

// checkScan collects the commands of a Check section line by line.
type checkScan struct {
	commands, skipped []string
	fence             string
	continued         bool // the last command of the fence ended in a backslash
}

func (f *checkScan) add(line string) {
	if line == "" || line == "- none" {
		return
	}
	if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
		switch marker := line[:3]; f.fence {
		case "":
			f.fence = marker
		case marker:
			f.fence, f.continued = "", false
		}
		return
	}
	if f.fence != "" {
		f.fenced(line)
		return
	}
	command, ok := checkSpan(line)
	command = strings.TrimSpace(strings.TrimPrefix(command, "$ "))
	if !ok || command == "" {
		f.skipped = append(f.skipped, line)
		return
	}
	f.commands = append(f.commands, command)
}

// fenced adds a line inside a fenced block. A comment line is no command. A
// line ending in a backslash goes on in the next line: together they are one
// command.
func (f *checkScan) fenced(line string) {
	if strings.HasPrefix(line, "#") {
		return
	}
	text, more := strings.CutSuffix(line, `\`)
	text = strings.TrimSpace(text)
	if f.continued && len(f.commands) > 0 {
		f.commands[len(f.commands)-1] += " " + text
	} else if text = strings.TrimSpace(strings.TrimPrefix(text, "$ ")); text != "" {
		f.commands = append(f.commands, text)
	} else {
		f.continued = false
		return
	}
	f.continued = more
}

// checkSpan returns the text of line when line, with an optional list marker,
// is exactly one backtick span.
func checkSpan(line string) (string, bool) {
	if rest, ok := strings.CutPrefix(line, "- "); ok {
		line = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(line, "* "); ok {
		line = strings.TrimSpace(rest)
	}
	inner, ok := strings.CutPrefix(line, "`")
	if !ok {
		return "", false
	}
	inner, ok = strings.CutSuffix(inner, "`")
	if !ok || strings.Contains(inner, "`") {
		return "", false
	}
	return inner, true
}
