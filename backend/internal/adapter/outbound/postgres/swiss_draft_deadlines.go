package postgres

import deadline "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/deadline"

const SwissDraftDeadlineMaximumBatchSize = deadline.SwissDraftDeadlineMaximumBatchSize

type SwissDraftDeadlinePostgres = deadline.SwissDraftDeadlinePostgres

func NewSwissDraftDeadlinePostgres(
	tx *TxManager,
	drafts *ParticipantDraftRepository,
) *SwissDraftDeadlinePostgres {
	return deadline.NewSwissDraftDeadlinePostgres(tx, drafts)
}
