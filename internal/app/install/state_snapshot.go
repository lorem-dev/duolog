package install

import (
	"bytes"
	"fmt"
	"os"

	"github.com/lorem-dev/duolog/internal/store"
)

func readStateSnapshot(path string, source *[]byte) ([]byte, error) {
	old, err := os.ReadFile(path)
	if source != nil && (err == nil || os.IsNotExist(err)) && !bytes.Equal(old, *source) {
		return nil, fmt.Errorf("%s changed during install; retry", path)
	}
	return old, err
}

func replaceState(tmp, path string, old []byte) error {
	current, err := readIfExists(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, old) {
		return fmt.Errorf("%s changed during install; retry", path)
	}
	return store.Rename(tmp, path)
}

func (d Deps) resolveForInstall(o Options) ([]Agent, string, error) {
	agents, base, err := d.resolve(o)
	if err != nil {
		return nil, "", err
	}
	if !o.HooksOnly {
		if err := checkSkillDirs(d.Agents, o, base); err != nil {
			return nil, "", err
		}
	}
	return agents, base, nil
}
