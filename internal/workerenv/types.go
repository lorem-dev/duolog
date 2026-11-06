package workerenv

import "github.com/lorem-dev/duolog/internal/core/launch"

// The launch types live in core/launch; these names keep the importers of
// workerenv as they are.
type (
	Shape         = launch.Shape
	Variant       = launch.Variant
	Process       = launch.Process
	Signal        = launch.Signal
	Layer         = launch.Layer
	LayerFacts    = launch.LayerFacts
	ProviderFacts = launch.ProviderFacts
	ProfileFacts  = launch.ProfileFacts
	Locations     = launch.Locations
)

const (
	Worker = launch.Worker
	Peer   = launch.Peer
	Lead   = launch.Lead
	Free   = launch.Free
	Paid   = launch.Paid
)
