// The fake agent reads a scenario next to its own executable.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path + ".json")
	if err != nil {
		return err
	}
	var s testbin.Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	args, _ := json.Marshal(os.Args[1:])
	command := strings.Join(os.Args[1:], " ")
	longest, selected := choose(s, command)
	// Count earlier calls that chose the same scenario before recording this one.
	earlier := 0
	if prior, err := os.ReadFile(path + ".calls.ndjson"); err == nil {
		for _, line := range strings.Split(string(prior), "\n") {
			var words []string
			if json.Unmarshal([]byte(line), &words) != nil {
				continue
			}
			if p, _ := choose(s, strings.Join(words, " ")); p == longest {
				earlier++
			}
		}
	}
	calls, err := os.OpenFile(path+".calls.ndjson", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := calls.Write(append(args, '\n'))
	closeErr := calls.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	s = selected
	if len(s.Sequence) > 0 {
		s = s.Sequence[min(earlier, len(s.Sequence)-1)]
	}
	env, _ := json.Marshal(os.Environ())
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	for suffix, content := range map[string][]byte{".args.json": args, ".env.json": env, ".cwd": []byte(cwd), ".stdin": stdin} {
		if err := os.WriteFile(path+suffix, content, 0600); err != nil {
			return err
		}
	}
	for _, chunk := range s.Output {
		stream := os.Stdout
		if chunk.Stderr {
			stream = os.Stderr
		}
		fmt.Fprint(stream, chunk.Text)
		// Allow the runner to drain each pipe before emitting the next chunk.
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Fprint(os.Stdout, s.Stdout)
	fmt.Fprint(os.Stderr, s.Stderr)
	time.Sleep(time.Duration(s.SleepMS) * time.Millisecond)
	for dest, content := range s.Writes {
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0600); err != nil {
			return err
		}
	}
	if s.Exit < 0 {
		p, err := os.FindProcess(os.Getpid())
		if err != nil {
			return err
		}
		return p.Kill()
	}
	os.Exit(s.Exit)
	return nil
}

// choose selects the scenario whose prefix is the longest match for command.
func choose(s testbin.Scenario, command string) (string, testbin.Scenario) {
	longest := ""
	selected := s
	for prefix, response := range s.Responses {
		if (command == prefix || strings.HasPrefix(command, prefix+" ")) && len(prefix) > len(longest) {
			longest, selected = prefix, response
		}
	}
	return longest, selected
}
