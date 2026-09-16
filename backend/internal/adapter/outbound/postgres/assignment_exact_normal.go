package postgres

import (
	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
)

type ExactNormalAssignmentPostgres = assignmentadapter.ExactNormalAssignmentPostgres

func NewExactNormalAssignmentPostgres(tx *TxManager) *ExactNormalAssignmentPostgres {
	return assignmentadapter.NewExactNormalAssignmentPostgres(tx)
}
