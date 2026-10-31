// Package delegate coordinates task handoffs, reusable threads and one fallback.
package delegate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/task"
	"github.com/lorem-dev/duolog/internal/trust"
)

type Outcome struct {
	Result  runner.Result
	RunHere *Placement
	Line    string
}
type Placement struct {
	Where               string
	Model, Effort, Task string
}

func (p Placement) String() string {
	if p.Where == "self" {
		return "run here: self task=" + p.Task
	}
	return fmt.Sprintf("run here: subagent model=%s effort=%s task=%s", p.Model, p.Effort, p.Task)
}

type Options struct {
	Lead             string
	AllowManualModel bool
}
type optionsKey struct{}

func WithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

var runWorker = runner.Run
var resolveModel = models.Resolve

func Place(cfg *config.Config, taskPath string, route router.Route) (Placement, error) {
	root, err := os.Getwd()
	if err != nil {
		return Placement{}, err
	}
	return place(cfg, taskPath, root, route)
}
func place(cfg *config.Config, taskPath, root string, route router.Route) (Placement, error) {
	p := Placement{Where: "self", Model: route.Model, Effort: route.Effort, Task: taskPath}
	refs, err := task.References(taskPath)
	if err != nil {
		return p, err
	}
	files := make(map[string]bool)
	lines := 0
	for _, ref := range refs {
		files[ref.Path] = true
		data, err := ref.Text(root)
		if err != nil {
			p.Where = "subagent"
			return p, nil
		}
		lines += strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
	}
	if len(files) > cfg.Limits.SelfMaxReadFiles || lines > cfg.Limits.SelfMaxReadLines {
		p.Where = "subagent"
	}
	return p, nil
}

func Run(ctx context.Context, cfg *config.Config, s *store.Store, role, taskPath, thread string) (out Outcome, err error) {
	defer func() {
		if out.Line == "" && err != nil {
			out.Line = oneLine(fmt.Sprintf("role %s: %v", role, err))
		}
	}()
	if depth := os.Getenv("DUOLOG_DEPTH"); depth != "" && depth != "0" {
		return out, fmt.Errorf("nested delegation refused (DUOLOG_DEPTH=%s)", depth)
	}
	root, err := filepath.Abs(filepath.Dir(s.Dir))
	if err != nil {
		return out, err
	}
	source, err := task.Resolve(root, taskPath)
	if err != nil {
		return out, err
	}
	if err = task.Validate(source); err != nil {
		return out, err
	}
	original, err := os.ReadFile(source)
	if err != nil {
		return out, err
	}
	tasksDir := filepath.Join(s.Dir, "tasks")
	if err = os.MkdirAll(tasksDir, 0755); err != nil {
		return out, err
	}
	dir, err := os.MkdirTemp(tasksDir, "task-")
	if err != nil {
		return out, err
	}
	copyPath := filepath.Join(dir, "task.md")
	if err = os.WriteFile(copyPath, original, 0600); err != nil {
		return out, err
	}
	if err = os.WriteFile(filepath.Join(dir, "schema.json"), []byte(task.Schema), 0600); err != nil {
		return out, err
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		return out, err
	}
	opts, _ := ctx.Value(optionsKey{}).(Options)
	leadID := opts.Lead
	if leadID == "" {
		leadID = session.CurrentLead(os.Environ())
	}
	lead, hasLead := reg.Lead(leadID)
	var leadTarget *config.Target
	leadMode := mode.Ask
	address := "duolog/lead"
	if hasLead {
		leadTarget = &lead.Target
		leadMode = lead.Mode
		address = session.Address(lead)
	} else {
		leadID = ""
	}
	reg.LeadID = leadID
	b, err := bus.Open(s.Dir, cfg.Wire)
	if err != nil {
		return out, err
	}
	ref := filepath.ToSlash(filepath.Join("tasks", filepath.Base(dir), "task.md"))
	worker := "duolog/worker"
	warning := ""
	var firstFailure error
	send := func(from, to, kind, text, reference string) error {
		_, e := b.Send(bus.Message{From: from, To: to, K: kind, Th: thread, Ref: reference, S: short(oneLine(text), cfg.Wire.MaxSummaryBytes)})
		return e
	}
	defer func() {
		if err != nil && firstFailure != nil {
			err = errors.Join(firstFailure, fmt.Errorf("%s: %w", worker, err))
		}
		if out.Line == "" && err != nil {
			out.Line = oneLine(fmt.Sprintf("role %s: %v", role, err))
		}
		if out.Line != "" {
			if sendErr := send(worker, address, "answer", out.Line, ref); sendErr != nil {
				err = errors.Join(err, sendErr)
			}
		}
	}()
	unavailable := reg.Unavailable()
	priorAgent := ""
	placementTask := taskPath
	for attempt := 0; attempt < 2; attempt++ {
		route, routeErr := router.Resolve(cfg, role, unavailable, router.Options{Lead: leadTarget, AllowManualModel: opts.AllowManualModel})
		if routeErr != nil {
			return out, routeErr
		}
		worker = route.Agent + "/" + role
		if route.IsFallback {
			from := priorAgent
			if from == "" {
				from = cfg.Roles[role].Use.Agent
			}
			if err = send(worker, address, "status", fmt.Sprintf("role %s moved from %s to %s model=%s effort=%s", role, from, route.Agent, route.Model, route.Effort), ref); err != nil {
				return out, err
			}
		}
		if leadMode == mode.Plan && route.Sandbox != "read-only" {
			return out, fmt.Errorf("mode plan refuses write role %s", role)
		}
		if route.Self || route.InLead {
			p := Placement{Where: "self", Task: placementTask}
			if !route.Self {
				p, err = place(cfg, placementTask, root, route)
				if err != nil {
					return out, err
				}
			}
			out.RunHere = &p
			out.Line = p.String()
			return out, nil
		}
		state, trustErr := trust.Hooks(route.Agent, root)
		if trustErr != nil {
			return out, trustErr
		}
		folder, trustErr := trust.Folder(route.Agent, root)
		if trustErr != nil {
			return out, trustErr
		}
		warning = ""
		if !state.Trusted {
			if route.Sandbox != "read-only" {
				return out, fmt.Errorf("Codex hooks untrusted; open /hooks before running write role %s", role)
			}
			warning = "; rules are off; open /hooks"
		}
		if !folder {
			warning += "; folder trust missing"
		}
		inline, inlineErr := task.Inline(copyPath, root, cfg.Wire.InlineMaxBytes)
		if inlineErr != nil {
			return out, inlineErr
		}
		model, modelErr := resolveModel(ctx, cfg, filepath.Join(s.Dir, "cache"), route.Target)
		if modelErr != nil {
			if missingBinary(modelErr) {
				err = modelErr
			} else {
				return out, modelErr
			}
		} else {
			route.Model = model
			spec := runner.Spec{Route: route, Mode: leadMode, TaskDir: dir, Cwd: root, Stdin: inline, Env: workerEnv(cfg.EnvPassthrough, leadID), Persistent: route.Session == "resume"}
			if route.Sandbox == "read-only" {
				spec.Mode = mode.Plan
			}
			var entry *session.Entry
			if route.Session == "resume" {
				entry, _ = reg.Lookup(leadID, route.Agent, role, thread)
				if entry != nil {
					over := entry.Input > int64(cfg.Sessions.ResumeMaxInputTokens)
					idle := cfg.Sessions.IdleCloseMinutes > 0 && time.Since(entry.UpdatedAt) > time.Duration(cfg.Sessions.IdleCloseMinutes)*time.Minute
					if over || idle {
						if err = reg.Close(entry.ID); err != nil {
							return out, err
						}
						entry = nil
					} else {
						spec.ThreadID = entry.ThreadID
					}
				}
			}
			if err = send(address, worker, "task", "role "+role+" task="+ref, ref); err != nil {
				return out, err
			}
			before, snapErr := snapshot(root)
			if snapErr != nil {
				return out, snapErr
			}
			out.Result, err = runWorker(ctx, spec)
			if usageErr := appendUsage(s.Dir, role, route, out.Result); usageErr != nil {
				return out, errors.Join(err, usageErr)
			}
			if out.Result.Interrupted {
				out.Line = "role " + role + " interrupted"
				if err == nil {
					err = fmt.Errorf("worker interrupted")
				}
				return out, err
			}
			if !out.Result.QuotaEnded && err == nil {
				sessionID := out.Result.SessionID
				if sessionID == "" && entry != nil {
					sessionID = entry.ThreadID
				}
				if route.Session == "resume" && sessionID != "" {
					e := session.Entry{ID: filepath.Base(dir), Lead: leadID, Agent: route.Agent, Role: role, Thread: thread, ThreadID: sessionID, Input: out.Result.Usage.Input}
					if entry != nil {
						e.ID = entry.ID
						e.Input += entry.Input
					}
					if err = reg.RecordLimited(e, cfg.Limits.MaxLiveSessions); err != nil {
						return out, err
					}
				}
				out.Line = fmt.Sprintf("done=%t summary=%s files=%s%s", out.Result.Done, oneLine(out.Result.Summary), strings.Join(out.Result.Files, ","), warning)
				return out, nil
			}
			if out.Result.QuotaEnded {
				if markErr := reg.MarkUnavailable(route.Agent, out.Result.ResetAt); markErr != nil {
					return out, errors.Join(err, markErr)
				}
				if entry != nil {
					if closeErr := reg.Close(entry.ID); closeErr != nil {
						return out, errors.Join(err, closeErr)
					}
				}
				if err == nil {
					err = fmt.Errorf("quota ended")
				}
				after, snapErr := snapshot(root)
				if snapErr != nil {
					return out, errors.Join(err, snapErr)
				}
				changed := changes(before, after)
				if len(changed) > 0 {
					note := "Partial work from " + route.Agent + ": " + strings.Join(changed, ", ") + ". Inspect these files before continuing.\n"
					updated := strings.Replace(strings.ReplaceAll(string(original), "\r\n", "\n"), "## Limits\n", "## Limits\n"+note, 1)
					if writeErr := os.WriteFile(copyPath, []byte(updated), 0600); writeErr != nil {
						return out, writeErr
					}
					placementTask = copyPath
				}
			} else if !missingBinary(err) {
				return out, err
			}
		}
		failure := fmt.Errorf("%s: %w", route.Agent, err)
		if attempt == 1 {
			return out, failure
		}
		firstFailure = failure
		priorAgent = route.Agent
		unavailable[route.Agent] = true
	}
	return out, firstFailure
}

func missingBinary(err error) bool {
	var pathErr *os.PathError
	return errors.Is(err, exec.ErrNotFound) || (errors.As(err, &pathErr) && pathErr.Op == "fork/exec" && errors.Is(err, os.ErrNotExist))
}
func workerEnv(names []string, leadID string) []string {
	var env []string
	for _, name := range names {
		if name == "DUOLOG_DEPTH" || name == "DUOLOG_LEAD" {
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "DUOLOG_DEPTH=1", "DUOLOG_LEAD="+leadID)
}
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type usageRecord struct {
	Role         string `json:"role"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	EffortCapped bool   `json:"effort_capped"`
	Input        int64  `json:"input"`
	Cached       int64  `json:"cached"`
	Output       int64  `json:"output"`
}

func appendUsage(dir, role string, route router.Route, result runner.Result) error {
	lock := flock.New(filepath.Join(dir, "usage.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(dir, "usage.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(f).Encode(usageRecord{role, route.Agent, route.Model, route.Effort, route.EffortCapped, result.Usage.Input, result.Usage.Cached, result.Usage.Output})
	return errors.Join(writeErr, f.Close())
}
func snapshot(root string) (map[string][32]byte, error) {
	result := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".duolog" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = sha256.Sum256(data)
		return nil
	})
	return result, err
}
func changes(before, after map[string][32]byte) []string {
	var names []string
	for path, hash := range after {
		if old, ok := before[path]; !ok || old != hash {
			names = append(names, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			names = append(names, path)
		}
	}
	sort.Strings(names)
	return names
}
