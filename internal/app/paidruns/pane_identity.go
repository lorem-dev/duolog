package paidruns

import (
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
)

func samePaneIdentity(open paidrules.Reservation, process launch.Process, accountKey string) bool {
	return open.Route == RouteKey(process) && open.AccountKey == accountKey
}
