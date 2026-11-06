// Package models resolves vendor model names without making model calls.
package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/route"
)

// Resolver permits tests and callers to supply a catalog without running Codex.
// List returns the catalog and the Codex version that supplied it.
type Resolver struct {
	CacheDir string
	List     func(context.Context) ([]route.Entry, string, error)
}

type catalogCache struct {
	Models    []route.Entry `json:"models"`
	Version   string        `json:"version"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Resolve uses the standard Codex metadata commands and a directory cache.
func Resolve(ctx context.Context, cfg *config.Config, cacheDir string, target config.Target) (string, error) {
	return (Resolver{CacheDir: cacheDir}).Resolve(ctx, cfg, target)
}

func (r Resolver) Resolve(ctx context.Context, cfg *config.Config, target config.Target) (string, error) {
	if target.Agent == "claude" {
		if strings.HasPrefix(target.Model, "claude-") {
			return target.Model, nil
		}
		return route.Normalize(target.Agent, target.Model), nil
	}
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
	cached := r.readCache()
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
	if err := r.writeCache(catalogCache{Models: list, Version: version, UpdatedAt: time.Now().UTC()}); err != nil {
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

func (r Resolver) readCache() catalogCache {
	var c catalogCache
	if r.CacheDir == "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(r.CacheDir, "models.json"))
	if err == nil {
		if json.Unmarshal(data, &c) != nil {
			return catalogCache{}
		}
	}
	return c
}

func (r Resolver) writeCache(c catalogCache) error {
	if r.CacheDir == "" {
		return nil
	}
	if err := os.MkdirAll(r.CacheDir, 0755); err != nil {
		return fmt.Errorf("model cache: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(r.CacheDir, "models-*.json")
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
	if err := store.Rename(file.Name(), filepath.Join(r.CacheDir, "models.json")); err != nil {
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
