package postgres

import (
	playoffrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/playoff"
)

// PlayoffTerminalPostgres keeps the historical root-package name while the
// terminal-stage implementation lives in the playoff capability package.
type PlayoffTerminalPostgres = playoffrepo.PlayoffTerminalPostgres

func NewPlayoffTerminalPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
	assignments *AssignmentPostgres,
) *PlayoffTerminalPostgres {
	var draftRepository playoffrepo.DraftRepository
	if drafts != nil {
		draftRepository = drafts
	}
	var createAssignmentTx playoffrepo.AssignmentWriter
	if assignments != nil {
		createAssignmentTx = assignments.CreateAssignmentTx
	}
	return playoffrepo.NewPlayoffTerminalPostgres(tx, draftRepository, createAssignmentTx)
}
