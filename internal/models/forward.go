// Package models forwards to the model catalog in internal/core/route and the
// resolver in internal/adapter/models, for the files that keep their imports
// until they move: internal/paidruns (Normalize) and internal/peer/peer.go
// (Normalize and Resolve).
package models

import (
	adaptermodels "github.com/lorem-dev/duolog/internal/adapter/models"
	"github.com/lorem-dev/duolog/internal/core/route"
)

var (
	Normalize = route.Normalize
	Resolve   = adaptermodels.Resolve
)
