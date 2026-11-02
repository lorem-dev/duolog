// Package coauthor records worker contributions and formats Git trailers.
package coauthor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

type Record struct {
	Time  time.Time `json:"time"`
	Lead  string    `json:"lead"`
	Agent string    `json:"agent"`
	Model string    `json:"model"`
	Files int       `json:"files"`
	Peer  string    `json:"peer,omitempty"`
}

// Append serializes appends and the once-per-peer check under the same lock.
// dir is the project's .duolog directory, as for usage.ndjson.
func Append(dir string, cfg config.Coauthors, r Record) error {
	if !cfg.Enabled || workerenv.IsWorker() || r.Files <= 0 {
		return nil
	}
	lock := flock.New(filepath.Join(dir, "coauthors.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	path := filepath.Join(dir, "coauthors.ndjson")
	if r.Peer != "" {
		records, err := read(path)
		if err != nil {
			return err
		}
		for _, have := range records {
			if have.Peer == r.Peer {
				return nil
			}
		}
	}
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	r.Time = r.Time.UTC()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return errors.Join(json.NewEncoder(f).Encode(r), f.Close())
}

func read(path string) ([]Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var records []Record
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		var r Record
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, scan.Err()
}

func displayName(model string) string {
	parts := strings.Split(model, "-")
	start := len(parts)
	for start > 0 {
		part := parts[start-1]
		if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			break
		}
		start--
	}
	words := make([]string, 0, len(parts))
	for _, part := range parts[:start] {
		runes := []rune(part)
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		words = append(words, string(runes))
	}
	if start < len(parts) {
		words = append(words, strings.Join(parts[start:], "."))
	}
	return strings.Join(words, " ")
}

// Lines returns distinct trailers in record order, strictly after since.
func Lines(dir string, cfg config.Coauthors, since time.Time) ([]string, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	records, err := read(filepath.Join(dir, "coauthors.ndjson"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var lines []string
	for _, r := range records {
		if !r.Time.After(since) || r.Files <= 0 {
			continue
		}
		var template string
		switch r.Agent {
		case "claude":
			template = cfg.Claude
		case "codex":
			template = cfg.Codex
		}
		line := strings.TrimSpace(strings.NewReplacer("{model}", r.Model, "{name}", displayName(r.Model)).Replace(template))
		if line == "" {
			continue
		}
		if strings.ContainsAny(line, "\r\n") {
			return nil, fmt.Errorf("co-author template must be one line")
		}
		line = "Co-Authored-By: " + line
		if !seen[line] {
			lines = append(lines, line)
			seen[line] = true
		}
	}
	return lines, nil
}

// Since uses the working repository, so a linked worktree uses its own HEAD.
func Since(root, rev string) (time.Time, error) {
	data, err := exec.Command("git", "-C", root, "show", "-s", "--format=%ct", "--end-of-options", rev+"^{commit}", "--").Output()
	if err != nil {
		if rev == "HEAD" {
			if _, headErr := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD").Output(); headErr != nil {
				if _, symErr := exec.Command("git", "-C", root, "symbolic-ref", "-q", "HEAD").Output(); symErr == nil {
					return time.Time{}, nil
				}
			}
		}
		return time.Time{}, fmt.Errorf("committer time of %q: %w", rev, err)
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

var trailer = regexp.MustCompile(`^[A-Za-z0-9-]+:[ \t]+\S`)

func hasTrailerBlock(body string) bool {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	i := strings.LastIndex(body, "\n\n")
	if i < 0 {
		return false
	}
	lines := strings.Split(body[i+2:], "\n")
	if !trailer.MatchString(lines[0]) {
		return false
	}
	for _, line := range lines[1:] {
		if !trailer.MatchString(line) && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			return false
		}
	}
	return true
}

// Hook preserves the message when all trailers are present or called by a worker.
func Hook(path string, lines []string) error {
	if workerenv.IsWorker() || len(lines) == 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	existing := strings.Split(text, "\n")
	var missing []string
	for _, line := range lines {
		found := false
		for _, have := range existing {
			if strings.EqualFold(strings.TrimSpace(have), line) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, line)
			existing = append(existing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	body := strings.TrimRight(text, "\r\n")
	hasTrailers := hasTrailerBlock(body)
	if body != "" {
		body += eol
		if !hasTrailers {
			body += eol
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(body + strings.Join(missing, eol) + eol)
	return errors.Join(err, f.Close())
}
