package progression

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// progressionLogicalNode is the normalized result-node lineage selected by
// LockTournamentProgressionSwissLogicalResultNodes. It intentionally keeps
// the physical source ID separate from the logical node identity: ordinary
// commits use the former directly, while correction commits require a durable
// binding to a successor node.
type progressionLogicalNode struct {
	Kind                 domain.ArtifactKind
	EntityID             uuid.UUID
	SourceID             uuid.UUID
	NodeID               uuid.UUID
	AuthoritySource      string
	HasCorrectionBinding bool
}

func indexProgressionLogicalNodes(
	nodes []progressionLogicalNode,
) (map[uuid.UUID]progressionLogicalNode, error) {
	indexed := make(map[uuid.UUID]progressionLogicalNode, len(nodes))
	for _, node := range nodes {
		if !node.Kind.IsValid() || node.EntityID == uuid.Nil || node.SourceID == uuid.Nil ||
			node.NodeID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		switch node.AuthoritySource {
		case "result_commit", "wave_initialization":
			if node.HasCorrectionBinding || node.NodeID != node.SourceID {
				return nil, domain.ErrConflict
			}
		case "correction_commit":
			if !node.HasCorrectionBinding {
				return nil, domain.ErrConflict
			}
		default:
			return nil, domain.ErrConflict
		}
		if _, duplicate := indexed[node.SourceID]; duplicate {
			return nil, domain.ErrConflict
		}
		indexed[node.SourceID] = node
	}
	return indexed, nil
}
