// Package models forwards to the model catalog in internal/core/route for
// internal/paidruns, which keeps its imports until it moves.
package models

import "github.com/lorem-dev/duolog/internal/core/route"

var Normalize = route.Normalize
