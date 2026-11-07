package kit

import (
	"errors"
	"os"
	"os/exec"
)

// StartDetached starts argv in dir as a process of its own (Detach) that the
// caller does not wait for, and appends its output to the file at logPath.
// What the log held stays: a process that truncates it does so itself. The
// child is reaped when it ends, in a caller that lives on.
func StartDetached(argv []string, dir, logPath string) error {
	if len(argv) == 0 {
		return errors.New("kit: nothing to start")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
