package catalog

import (
	"crypto/sha256"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type DeterministicIDGenerator struct {
	namespace uuid.UUID
}

func NewDeterministicIDGenerator(namespace uuid.UUID) (*DeterministicIDGenerator, error) {
	if namespace == uuid.Nil {
		return nil, domain.ErrValidation
	}
	return &DeterministicIDGenerator{namespace: namespace}, nil
}

func (g *DeterministicIDGenerator) Derive(in IDInput) (uuid.UUID, error) {
	if g == nil || g.namespace == uuid.Nil || !in.Scope.isValid() || in.IdempotencyKey == uuid.Nil {
		return uuid.Nil, domain.ErrValidation
	}
	document := string(in.Scope) + "\x00" + in.IdempotencyKey.String() + "\x00"
	return uuid.NewHash(sha256.New(), g.namespace, []byte(document), 5), nil
}

func (s IDScope) isValid() bool {
	switch s {
	case IDScopeTournament,
		IDScopeRoster:
		return true
	default:
		return false
	}
}

var _ IDGenerator = (*DeterministicIDGenerator)(nil)
