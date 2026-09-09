package playoff

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type finalSwissPayloadDocument struct {
	TournamentID       uuid.UUID                       `json:"tournament_id"`
	ProjectionID       uuid.UUID                       `json:"projection_id"`
	Preset             domain.TournamentPreset         `json:"preset"`
	RevisionNo         int                             `json:"revision_no"`
	PreviousRevisionID *uuid.UUID                      `json:"previous_revision_id,omitempty"`
	CreatedAt          time.Time                       `json:"created_at"`
	Entries            []FinalSwissStanding            `json:"entries"`
	TieGroups          []FinalSwissTieGroup            `json:"tie_groups"`
	GoldenGroups       []FinalSwissGoldenGroupIdentity `json:"golden_groups"`
	GoldenSourceDigest string                          `json:"golden_source_digest"`
	Rounds             []finalSwissRoundPayload        `json:"rounds"`
	Series             []finalSwissSeriesPayload       `json:"series"`
}

type finalSwissRoundPayload struct {
	RoundID          uuid.UUID  `json:"round_id"`
	RoundNumber      int        `json:"round_number"`
	RevisionID       uuid.UUID  `json:"revision_id"`
	LockProofHash    string     `json:"lock_proof_hash"`
	ByeRevisionID    *uuid.UUID `json:"bye_revision_id,omitempty"`
	ByeParticipantID *uuid.UUID `json:"bye_participant_id,omitempty"`
}

type finalSwissSeriesPayload struct {
	RoundID                  uuid.UUID                      `json:"round_id"`
	RoundNumber              int                            `json:"round_number"`
	SeriesID                 uuid.UUID                      `json:"series_id"`
	FirstParticipantID       uuid.UUID                      `json:"first_participant_id"`
	SecondParticipantID      uuid.UUID                      `json:"second_participant_id"`
	ScoreRevisionID          uuid.UUID                      `json:"score_revision_id"`
	ScoreCommandID           uuid.UUID                      `json:"score_command_id"`
	ScoreProjectionRevision  uuid.UUID                      `json:"score_projection_revision"`
	ScoreProjectionDigest    string                         `json:"score_projection_digest"`
	OfficialCommandID        uuid.UUID                      `json:"official_command_id"`
	OfficialResultRevision   uuid.UUID                      `json:"official_result_revision"`
	OfficialSourceDigest     string                         `json:"official_source_digest"`
	ProjectionRevision       uuid.UUID                      `json:"projection_revision"`
	ProjectionPayloadDigest  string                         `json:"projection_payload_digest"`
	State                    string                         `json:"state"`
	WinnerID                 uuid.UUID                      `json:"winner_id"`
	OfficialResultRecordedAt time.Time                      `json:"official_result_recorded_at"`
	ResultLabel              swissusecase.SeriesResultLabel `json:"result_label"`
	FirstEffectiveTime       int64                          `json:"first_effective_time_ns"`
	SecondEffectiveTime      int64                          `json:"second_effective_time_ns"`
	FirstAcceptedSolveTime   *int64                         `json:"first_accepted_solve_time_ns,omitempty"`
	SecondAcceptedSolveTime  *int64                         `json:"second_accepted_solve_time_ns,omitempty"`
	NoGame                   bool                           `json:"no_game"`
	NoGameEvidenceDigest     string                         `json:"no_game_evidence_digest,omitempty"`
}

func finalSwissPayload(
	authority finalSwissAuthority,
	standings []FinalSwissStanding,
	ties []FinalSwissTieGroup,
	heads []terminalSeriesRecord,
	goldenSource goldenusecase.StandingsProjection,
) ([]byte, error) {
	document := finalSwissPayloadDocument{
		TournamentID: authority.TournamentID, ProjectionID: authority.ProjectionID, Preset: authority.Preset,
		RevisionNo: authority.RevisionNo, CreatedAt: authority.CreatedAt,
		Entries: cloneFinalSwissStandings(standings), TieGroups: cloneFinalSwissTieGroups(ties),
		GoldenGroups: append([]FinalSwissGoldenGroupIdentity(nil), authority.GoldenGroups...),
		Rounds:       make([]finalSwissRoundPayload, len(authority.Rounds)),
		Series:       make([]finalSwissSeriesPayload, len(heads)),
	}
	document.GoldenSourceDigest = hex.EncodeToString(goldenSource.PayloadDigest[:])
	for index, round := range authority.Rounds {
		document.Rounds[index] = finalSwissRoundPayload{
			RoundID: round.RoundID, RoundNumber: round.RoundNumber, RevisionID: round.RevisionID,
			LockProofHash: round.LockProof.ProofHash,
		}
		if round.Bye != nil {
			value := round.Bye.RevisionID
			document.Rounds[index].ByeRevisionID = &value
			participantID := round.Bye.ParticipantID
			document.Rounds[index].ByeParticipantID = &participantID
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	for index, head := range heads {
		seriesPayload, err := finalSwissSeriesPayloadFromHead(head)
		if err != nil {
			return nil, err
		}
		document.Series[index] = seriesPayload
	}
	sort.Slice(document.Series, func(i, j int) bool {
		return bytes.Compare(document.Series[i].SeriesID[:], document.Series[j].SeriesID[:]) < 0
	})
	return json.Marshal(document)
}

func finalSwissSeriesPayloadFromHead(head terminalSeriesRecord) (finalSwissSeriesPayload, error) {
	revision := head.Projection.Revision()
	digest := revision.PayloadDigest()
	scoreRevisionID := uuid.Nil
	if head.Series.CurrentScoreRevisionID != nil {
		scoreRevisionID = head.Series.CurrentScoreRevisionID.UUID()
	}
	winnerID := uuid.Nil
	if head.Series.WinnerID != nil {
		winnerID = *head.Series.WinnerID
	}
	officialResultID := head.OfficialResult.Result.ID.UUID()
	officialCommandID := head.OfficialResult.Result.CommandID
	officialRecordedAt := head.OfficialResult.Result.RecordedAt
	officialDigest := head.OfficialResult.Result.SourceProjection.PayloadDigest()
	scoreCommandID := uuid.Nil
	scoreProjection := head.OfficialResult.ScoreProjection
	noGameEvidenceDigest := ""
	if head.OfficialResult.NoGame != nil {
		recorded := *head.OfficialResult.NoGame
		officialResultID = recorded.Series.ID.UUID()
		officialCommandID = recorded.CommandID
		officialRecordedAt = recorded.ResolvedAt
		officialDigest = recorded.ResultProjection.Revision().PayloadDigest()
		scoreCommandID = recorded.CommandID
		scoreProjection = &recorded.ScoreProjection
		noGameEvidenceDigest = head.NoGameEvidenceDigest
	} else if head.OfficialResult.Score != nil {
		scoreCommandID = head.OfficialResult.Score.CommandID
	}
	if scoreProjection == nil {
		return finalSwissSeriesPayload{}, finalSwissError("terminal Series has no score projection payload evidence")
	}
	scoreProjectionRevision := scoreProjection.Revision()
	scoreProjectionDigest := scoreProjectionRevision.PayloadDigest()
	return finalSwissSeriesPayload{
		RoundID: head.Result.RoundID, RoundNumber: head.Result.RoundNumber,
		SeriesID:                head.Result.SeriesID,
		FirstParticipantID:      head.Result.FirstParticipantID,
		SecondParticipantID:     head.Result.SecondParticipantID,
		ScoreRevisionID:         scoreRevisionID,
		ScoreCommandID:          scoreCommandID,
		ScoreProjectionRevision: scoreProjectionRevision.ID().UUID(),
		ScoreProjectionDigest:   hex.EncodeToString(scoreProjectionDigest[:]),
		OfficialCommandID:       officialCommandID,
		OfficialResultRevision:  officialResultID,
		OfficialSourceDigest:    hex.EncodeToString(officialDigest[:]),
		ProjectionRevision:      revision.ID().UUID(),
		ProjectionPayloadDigest: hex.EncodeToString(digest[:]),
		State:                   string(head.Series.State), WinnerID: winnerID,
		OfficialResultRecordedAt: officialRecordedAt,
		ResultLabel:              head.Result.Label,
		FirstEffectiveTime:       int64(head.Result.FirstEffectiveTime),
		SecondEffectiveTime:      int64(head.Result.SecondEffectiveTime),
		FirstAcceptedSolveTime:   finalSwissDurationNanos(head.Result.FirstAcceptedSolveTime),
		SecondAcceptedSolveTime:  finalSwissDurationNanos(head.Result.SecondAcceptedSolveTime),
		NoGame:                   head.OfficialResult.NoGame != nil,
		NoGameEvidenceDigest:     noGameEvidenceDigest,
	}, nil
}

func finalSwissDurationNanos(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	nanos := int64(*value)
	return &nanos
}
