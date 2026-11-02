package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMainClearsWorkerEnvironment(t *testing.T) {
	if os.Getenv("DUOLOG_TEST_CHECK_ENV") == "1" {
		for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
			if os.Getenv(key) != "" {
				t.Errorf("inherited %s", key)
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainClearsWorkerEnvironment$")
	cmd.Env = append(os.Environ(), "DUOLOG_TEST_CHECK_ENV=1", "DUOLOG_DEPTH=2", "DUOLOG_LEAD=other", "DUOLOG_ROLE=implement")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
}

func TestMain(m *testing.M) {
	// Each test process starts as an independent lead; tests set worker roles explicitly.
	for _, key := range []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE"} {
		if err := os.Unsetenv(key); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}
