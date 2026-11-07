package peer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

func setup(ctx context.Context) (*env, error) { return setupFor(ctx, true) }

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
	client, ok := rex.Detect()
	if !ok {
		return nil, errors.New("peer sessions need Rex: no rex executable in PATH, REX_BIN_DIR or the Rex app")
	}
	e, err := openRecords(opts, requireLead)
	if err != nil {
		return nil, err
	}
	client.Dir = e.root
	e.rex = client
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
	cfg := opts.Config
	if cfg == nil {
		if cfg, err = configfile.Load(root); err != nil {
			return nil, err
		}
	}
	s, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	reg, err := session.Open(s.Dir)
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
	if opts.WorkDir != "" {
		root = project.WorkRoot(opts.WorkDir, root)
	} else {
		root = project.CurrentWorkRoot(root)
	}
	return &env{opts: opts, root: root, dir: s.Dir, cfg: cfg, reg: reg, leadID: leadID}, nil
}
