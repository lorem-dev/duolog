package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

func setup(ctx context.Context) (*env, error) { return setupFor(ctx, true) }

// WorkRoot returns the peer's registered checkout. Older entries without
// one keep the caller's checkout, as before.
func WorkRoot(ctx context.Context, id string) (string, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return "", err
	}
	e, err := openRecords(opts, false)
	if err != nil {
		return "", err
	}
	entry, err := e.find(id)
	if err != nil {
		return "", err
	}
	if entry.WorkRoot != "" {
		return entry.WorkRoot, nil
	}
	return e.root, nil
}

func setupFor(ctx context.Context, requireLead bool) (*env, error) {
	e, err := setupRecords(ctx, requireLead)
	if err != nil {
		return nil, err
	}
	if e.bus, err = bus.Open(e.dir, e.cfg.Wire); err != nil {
		return nil, err
	}
	return e, nil
}

// setupViewer is the setup of the passive viewers, show and view. It needs
// no lead and opens no bus, so it creates no bus state and works with a bus
// the caller may only read. e.bus is nil.
func setupViewer(ctx context.Context) (*env, error) { return setupRecords(ctx, false) }

// setupRecords checks the caller and opens the registry and Rex, but not the
// bus.
func setupRecords(ctx context.Context, requireLead bool) (*env, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return nil, err
	}
	if os.Getenv("REX_SESSION") == "" {
		return nil, errors.New("peer sessions need Rex: REX_SESSION is not set; run this command in a Rex pane")
	}
	leadSession, ok := opts.Deps.Pane.Lead()
	if !ok {
		return nil, errors.New("peer sessions need Rex: no rex executable in PATH, REX_BIN_DIR or the Rex app")
	}
	e, err := openRecords(opts, requireLead)
	if err != nil {
		return nil, err
	}
	e.panes, e.leadSession = opts.Deps.Pane, leadSession
	return e, nil
}

// callerOptions refuses workers and reads the command's options.
func callerOptions(ctx context.Context) (Options, error) {
	if depth, _ := strconv.Atoi(os.Getenv("DUOLOG_DEPTH")); depth >= 1 {
		return Options{}, fmt.Errorf("workers cannot open or control peer sessions (DUOLOG_DEPTH=%d)", depth)
	}
	opts, _ := ctx.Value(optionsKey{}).(Options)
	if opts.Root == "" {
		return Options{}, errors.New("peer: missing project root")
	}
	return opts, nil
}

// openRecords opens the configuration, the state folder and the registry,
// and resolves the lead. It needs no Rex. The work root is the checkout of
// opts.WorkDir, or else of the command's working folder.
func openRecords(opts Options, requireLead bool) (*env, error) {
	root, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, err
	}
	work := project.CurrentWorkRoot(root)
	if opts.WorkDir != "" {
		work = project.WorkRoot(opts.WorkDir, root)
	}
	stateRoot := project.StateRoot(work)
	cfg := opts.Config
	if cfg == nil || !sameWorkRoot(project.StateRoot(root), stateRoot) {
		if cfg, err = configfile.Load(stateRoot); err != nil {
			return nil, err
		}
	}
	s, err := store.Open(stateRoot)
	if err != nil {
		return nil, err
	}
	reg, err := session.OpenLead(project.LeadHome(work), cfg, stateRoot)
	if err != nil {
		return nil, err
	}
	leadID := opts.Lead
	if requireLead {
		lead, err := reg.ResolveLead(opts.Lead)
		if err != nil {
			return nil, err
		}
		leadID = lead.ID
	}
	return &env{opts: opts, root: work, dir: reg.Dir, workDir: s.Dir, cfg: cfg, reg: reg, leadID: leadID, maxLiveSessions: reg.Limit}, nil
}

// forEntry keeps shared lead resources while selecting the peer's work rules.
func (e *env) forEntry(entry session.Entry) (*env, error) {
	copy := *e
	copy.root = e.peerRoot(entry)
	copy.workDir = e.workState(entry)
	if !sameWorkRoot(copy.workDir, e.workState(session.Entry{})) {
		cfg, err := configfile.Load(filepath.Dir(copy.workDir))
		if err != nil {
			return nil, err
		}
		copy.cfg, copy.gate = cfg, nil
	}
	return &copy, nil
}

// workState falls back to the caller's work project for legacy entries.
func (e *env) workState(entry session.Entry) string {
	if e.workDir != "" && (entry.WorkRoot == "" || sameWorkRoot(entry.WorkRoot, e.root)) {
		return e.workDir
	}
	if entry.WorkRoot != "" {
		return filepath.Join(project.StateRoot(entry.WorkRoot), ".duolog")
	}
	return e.dir
}
