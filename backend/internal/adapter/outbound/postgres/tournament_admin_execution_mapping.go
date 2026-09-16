package postgres

import "github.com/google/uuid"

// tournamentAdminExecutionID remains a root-private identity bridge for
// unmoved Swiss-draft and progression materializers.
func tournamentAdminExecutionID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(role))
}
