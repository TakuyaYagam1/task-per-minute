package domain

type SeriesResultReason string

const (
	SeriesResultReasonScoreComplete       SeriesResultReason = "score_complete"
	SeriesResultReasonOperatorCorrection  SeriesResultReason = "operator_correction"
	SeriesResultReasonSeriesCancelled     SeriesResultReason = "series_cancelled"
	SeriesResultReasonTournamentCancelled SeriesResultReason = "tournament_cancelled"
)

func (r SeriesResultReason) IsLegalFor(state SeriesState) bool {
	switch state {
	case SeriesStateCompleted:
		return r == SeriesResultReasonScoreComplete ||
			r == SeriesResultReasonOperatorCorrection
	case SeriesStateCancelled:
		return r == SeriesResultReasonSeriesCancelled ||
			r == SeriesResultReasonTournamentCancelled ||
			r == SeriesResultReasonOperatorCorrection
	case SeriesStatePlanned,
		SeriesStateLocked,
		SeriesStateDraft,
		SeriesStateReady,
		SeriesStateActive,
		SeriesStateReplayRequired,
		SeriesStateTechnicalPause:
		return false
	default:
		return false
	}
}
