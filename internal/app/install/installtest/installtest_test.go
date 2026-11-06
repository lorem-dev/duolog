package installtest

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/app/install"
)

func TestInstallerContract(t *testing.T) {
	InstallerContract(t, func(*testing.T) Port { return &Installer{} })
	// The fake is an Installer.
	var _ install.Installer = &Installer{}
}
