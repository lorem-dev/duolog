package calibration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/store"
)

// RatesFile holds the measured quota rates under Dir; it is personal.
const RatesFile = "rates.json"

func ratesPath(stateDir string) string { return filepath.Join(Dir(stateDir), RatesFile) }

// ReadRates returns the measured rates by agent; no file means no rates.
func ReadRates(stateDir string) (map[string]Rate, error) {
	data, err := os.ReadFile(ratesPath(stateDir))
	if os.IsNotExist(err) {
		return map[string]Rate{}, nil
	}
	if err != nil {
		return nil, err
	}
	rates := map[string]Rate{}
	if err := json.Unmarshal(data, &rates); err != nil {
		return nil, fmt.Errorf("calibration %s: %w", RatesFile, err)
	}
	return rates, nil
}

// WriteRates replaces rates.json through a temporary file, like Run.Save.
func WriteRates(stateDir string, r map[string]Rate) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	dir := Dir(stateDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rates-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), ratesPath(stateDir))
}
