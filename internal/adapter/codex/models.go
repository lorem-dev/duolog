package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/store"
)

// Resolver resolves a Codex alias to the newest visible model without making a
// model call. With the zero value it runs the standard Codex metadata commands;
// List lets a test supply a catalog without running Codex. List returns the
// catalog and the Codex version that supplied it.
type Resolver struct {
	List func(context.Context) ([]route.Entry, string, error)
}

type catalogCache struct {
	Models    []route.Entry `json:"models"`
	Version   string        `json:"version"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Resolve gives the model of target: a pinned gpt- name as it is, an alias as
// the newest visible model of its family. cacheDir holds the catalog cache; ""
// keeps none.
func (r Resolver) Resolve(ctx context.Context, cfg *config.Config, cacheDir string, target config.Target) (string, error) {
	if target.Agent != "codex" {
		return "", fmt.Errorf("unknown agent %q", target.Agent)
	}
	if strings.HasPrefix(target.Model, "gpt-") {
		return target.Model, nil
	}
	model, ok := cfg.Models[target.Model]
	if !ok || model.Family == "" {
		return "", fmt.Errorf("unknown Codex model family %q", target.Model)
	}
	cached := readCache(cacheDir)
	var list []route.Entry
	var version string
	var err error
	if r.List != nil {
		list, version, err = r.List(ctx)
	} else {
		var data []byte
		data, err = metadata(ctx, "--version")
		version = strings.TrimSpace(string(data))
		if err == nil && fresh(cached, version) {
			return pick(model.Family, cached.Models)
		}
		if err == nil {
			list, err = listModels(ctx)
		}
	}
	if err != nil {
		if name, ok := route.Latest(model.Family, cached.Models); ok {
			return name, nil
		}
		return "", fmt.Errorf("list Codex models: %w", err)
	}
	if fresh(cached, version) {
		return pick(model.Family, cached.Models)
	}
	name, err := pick(model.Family, list)
	if err != nil {
		return "", err
	}
	if err := writeCache(cacheDir, catalogCache{Models: list, Version: version, UpdatedAt: time.Now().UTC()}); err != nil {
		return "", err
	}
	return name, nil
}

func pick(family string, list []route.Entry) (string, error) {
	if name, ok := route.Latest(family, list); ok {
		return name, nil
	}
	return "", fmt.Errorf("no visible Codex model for family %q", family)
}

func fresh(c catalogCache, version string) bool {
	age := time.Since(c.UpdatedAt)
	return len(c.Models) > 0 && c.Version == version && age >= 0 && age < 24*time.Hour
}

func readCache(dir string) catalogCache {
	var c catalogCache
	if dir == "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err == nil {
		if json.Unmarshal(data, &c) != nil {
			return catalogCache{}
		}
	}
	return c
}

func writeCache(dir string, c catalogCache) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("model cache: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, "models-*.json")
	if err != nil {
		return fmt.Errorf("model cache: %w", err)
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("model cache: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("model cache: %w", closeErr)
	}
	if err := store.Rename(file.Name(), filepath.Join(dir, "models.json")); err != nil {
		return fmt.Errorf("model cache: %w", err)
	}
	return nil
}

func metadata(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.WaitDelay = 200 * time.Millisecond
	cmd.Stdin = bytes.NewReader(nil)
	return cmd.Output()
}

func listModels(ctx context.Context) ([]route.Entry, error) {
	data, err := metadata(ctx, "debug", "models")
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Models []route.Entry `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode Codex catalog: %w", err)
	}
	return catalog.Models, nil
}
