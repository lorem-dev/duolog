package config

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// LoadError is a config file that does not load: the file, the line of the
// first problem when duolog can tell it (0 when it cannot) and the problem.
// Its text is the file and the problem, as before it had a type.
type LoadError struct {
	File string
	Line int
	Err  error
}

func (e *LoadError) Error() string { return e.File + ": " + e.Err.Error() }
func (e *LoadError) Unwrap() error { return e.Err }

// lineError carries the line of a YAML node with an error whose text stays
// the same.
type lineError struct {
	line int
	err  error
}

func (e *lineError) Error() string { return e.err.Error() }
func (e *lineError) Unwrap() error { return e.err }

func atLine(line int, err error) error { return &lineError{line: line, err: err} }

// yamlLine finds the line YAML names in its own messages, such as
// "yaml: line 2: did not find expected node content".
var yamlLine = regexp.MustCompile(`\bline (\d+):`)

// fileError makes err from file a LoadError and finds its line.
func fileError(file string, err error) error {
	le := &LoadError{File: file, Err: err}
	var at *lineError
	if errors.As(err, &at) {
		le.Line = at.line
	} else if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		le.Line, _ = strconv.Atoi(m[1])
	}
	return le
}

// Problem describes a Load error in one line for a hook or duolog doctor:
// "config error in <file>, line <n>: <message>", without the line when it is
// not known, and without the file for a problem of the merged settings.
func Problem(err error) string {
	var le *LoadError
	if !errors.As(err, &le) {
		return "config error: " + oneLine(err.Error())
	}
	where := le.File
	if le.Line > 0 {
		where += ", line " + strconv.Itoa(le.Line)
	}
	return "config error in " + where + ": " + oneLine(le.Err.Error())
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
