package authority

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

// MapExecutionAuthorityLease keeps lease mapping available to the recovery
// adapter while authority persistence lives in this child package.
func MapExecutionAuthorityLease(
	row sqlc.ExecutionAuthorityLease,
) (*authoritydomain.Lease, error) {
	return mapExecutionAuthorityLease(row)
}
