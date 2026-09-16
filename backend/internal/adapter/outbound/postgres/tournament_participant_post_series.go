package postgres

import participantpostseries "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/postseries"

type ParticipantPostSeriesRepository = participantpostseries.ParticipantPostSeriesRepository

func NewParticipantPostSeriesRepository(tx *TxManager) *ParticipantPostSeriesRepository {
	return participantpostseries.NewParticipantPostSeriesRepository(tx)
}
