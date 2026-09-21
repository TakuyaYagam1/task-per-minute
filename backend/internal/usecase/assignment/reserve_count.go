package assignment

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// assignmentCommandReserveCount derives the configured chain length from the
// active prefix of the deterministic command identities. The command keeps a
// fixed three-slot shape for wire and persistence compatibility; trailing
// identities must be empty for zero- and one-reserve configurations.
func assignmentCommandReserveCount(command ExactNormalAssignmentCommand) (int, error) {
	for index := 0; index <= domain.MaxAssignmentReserveCount; index++ {
		ids := [3][3]uuid.UUID{command.EdgeIDs, command.ReservationIDs, command.SnapshotIDs}
		allEmpty := true
		allPresent := true
		for _, group := range ids {
			if group[index] != uuid.Nil {
				allEmpty = false
			} else {
				allPresent = false
			}
		}
		if allEmpty {
			for trailing := index + 1; trailing <= domain.MaxAssignmentReserveCount; trailing++ {
				for _, group := range ids {
					if group[trailing] != uuid.Nil {
						return 0, fmt.Errorf("%w: reserve command identities are not a contiguous prefix", ErrInvalidExactNormalAssignment)
					}
				}
			}
			if index == 0 {
				return 0, fmt.Errorf("%w: primary command identities are missing", ErrInvalidExactNormalAssignment)
			}
			return index - 1, nil
		}
		if !allPresent {
			return 0, fmt.Errorf("%w: reserve command identities are not a contiguous prefix", ErrInvalidExactNormalAssignment)
		}
	}
	return domain.MaxAssignmentReserveCount, nil
}

// ReserveCount exposes the configured chain size encoded by a command. It is
// useful to command producers that need to size deterministic identity lists
// without depending on a persistence implementation.
func (c ExactNormalAssignmentCommand) ReserveCount() (int, error) {
	return assignmentCommandReserveCount(c)
}
