package codex_test

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"github.com/lorem-dev/duolog/internal/app/install/installtest"
)

func TestInstallerContract(t *testing.T) {
	installtest.InstallerContract(t, func(*testing.T) installtest.Port { return codex.Installer{} })
}
