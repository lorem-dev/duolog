// Package usage forwards to internal/core/usage for internal/app/paidruns, which
// keeps its imports until it moves.
package usage

import coreusage "github.com/lorem-dev/duolog/internal/core/usage"

type Tokens = coreusage.Tokens

var Cost = coreusage.Cost
