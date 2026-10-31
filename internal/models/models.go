// Package models resolves vendor model names without making model calls.
package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
)

// Entry contains the catalog fields needed for version selection.
type Entry struct {
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
}

// Resolver permits tests and callers to supply a catalog without running Codex.
// List returns the catalog and the Codex version that supplied it.
type Resolver struct {
	CacheDir string
	List     func(context.Context) ([]Entry, string, error)
}

type catalogCache struct {
	Models    []Entry   `json:"models"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Latest returns the highest numeric version of a visible GPT family.
func Latest(family string, list []Entry) (string, bool) {
	var best []int
	var name string
	for _, entry := range list {
		if entry.Visibility == "hide" {
			continue
		}
		version, modelFamily, ok := splitGPT(entry.Slug)
		if !ok || modelFamily != family {
			continue
		}
		if name == "" || newer(version, best) {
			name, best = entry.Slug, version
		}
	}
	return name, name != ""
}

func splitGPT(name string) ([]int, string, bool) {
	rest, ok := strings.CutPrefix(name, "gpt-")
	if !ok {
		return nil, "", false
	}
	version, family, ok := strings.Cut(rest, "-")
	if !ok || family == "" {
		return nil, "", false
	}
	var numbers []int
	for _, part := range strings.Split(version, ".") {
		if part == "" {
			return nil, "", false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return nil, "", false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, "", false
		}
		numbers = append(numbers, n)
	}
	return numbers, family, true
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// Normalize maps a concrete vendor id to its family alias.
func Normalize(agent, concrete string) string {
	if agent == "codex" {
		if _, family, ok := splitGPT(concrete); ok {
			return family
		}
	}
	if agent == "claude" {
		if rest, ok := strings.CutPrefix(concrete, "claude-"); ok {
			parts := strings.Split(rest, "-")
			for i := 1; i < len(parts); i++ {
				if _, err := strconv.Atoi(parts[i]); err == nil {
					return strings.Join(parts[:i], "-")
				}
			}
		}
	}
	return concrete
}

// Resolve uses the standard Codex metadata commands and a directory cache.
func Resolve(ctx context.Context, cfg *config.Config, cacheDir string, target config.Target) (string, error) {
	return (Resolver{CacheDir: cacheDir}).Resolve(ctx, cfg, target)
}

func (r Resolver) Resolve(ctx context.Context, cfg *config.Config, target config.Target) (string, error) {
	if target.Agent == "claude" {
		return Normalize(target.Agent, target.Model), nil
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
	var list []Entry
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
		if name, ok := Latest(model.Family, cached.Models); ok {
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

func pick(family string, list []Entry) (string, error) {
	if name, ok := Latest(family, list); ok {
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
	if err := os.Rename(file.Name(), filepath.Join(r.CacheDir, "models.json")); err != nil {
		return fmt.Errorf("model cache: %w", err)
	}
	return nil
}

func metadata(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Stdin = bytes.NewReader(nil)
	return cmd.Output()
}

func listModels(ctx context.Context) ([]Entry, error) {
	data, err := metadata(ctx, "debug", "models")
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Models []Entry `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode Codex catalog: %w", err)
	}
	return catalog.Models, nil
}
