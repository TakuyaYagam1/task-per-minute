package admin

import (
	inboundcontract "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	admininbound "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/inbound"
)

// NewInboundAdapter preserves the root admin facade while the transport
// adapter and leaf mappings live in their own capability package.
func NewInboundAdapter(next AdminService) inboundcontract.TournamentAdminUseCase {
	return admininbound.NewInboundAdapter(next)
}
