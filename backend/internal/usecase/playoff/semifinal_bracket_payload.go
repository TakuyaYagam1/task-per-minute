package playoff

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type semifinalBracketPayloadDocument struct {
	TournamentID       uuid.UUID               `json:"tournament_id"`
	RevisionNo         int                     `json:"revision_no"`
	PreviousRevisionID *uuid.UUID              `json:"previous_revision_id,omitempty"`
	Top4RevisionID     uuid.UUID               `json:"top4_revision_id"`
	Top4Digest         string                  `json:"top4_digest"`
	Locked             bool                    `json:"locked"`
	LockedAt           time.Time               `json:"locked_at"`
	LowerBracket       bool                    `json:"lower_bracket"`
	Rounds             []semifinalMatchPayload `json:"rounds"`
}

type semifinalMatchPayload struct {
	Position            int                 `json:"position"`
	SeriesID            uuid.UUID           `json:"series_id"`
	FirstParticipantID  uuid.UUID           `json:"first_participant_id"`
	SecondParticipantID uuid.UUID           `json:"second_participant_id"`
	Format              domain.SeriesFormat `json:"format"`
	State               domain.SeriesState  `json:"state"`
	FirstWins           int                 `json:"first_wins"`
	SecondWins          int                 `json:"second_wins"`
	WinnerPath          SemifinalWinnerPath `json:"winner_path"`
	LoserPath           SemifinalLoserPath  `json:"loser_path"`
}

func semifinalBracketPayload(
	authority semifinalBracketAuthority,
	matches []SemifinalMatch,
	lockedAt time.Time,
) ([]byte, error) {
	top4Revision := authority.Top4.Projection().Revision()
	digest := top4Revision.PayloadDigest()
	document := semifinalBracketPayloadDocument{
		TournamentID: authority.TournamentID, RevisionNo: authority.RevisionNo,
		Top4RevisionID: top4Revision.ID().UUID(), Top4Digest: hex.EncodeToString(digest[:]),
		Locked: true, LockedAt: lockedAt, LowerBracket: false,
		Rounds: make([]semifinalMatchPayload, len(matches)),
	}
	for index, match := range matches {
		document.Rounds[index] = semifinalMatchPayload{
			Position: match.Position, SeriesID: match.Series.ID,
			FirstParticipantID:  match.Series.FirstParticipantID,
			SecondParticipantID: match.Series.SecondParticipantID,
			Format:              match.Series.Format, State: match.Series.State,
			FirstWins:  match.Series.Score.FirstParticipantWins,
			SecondWins: match.Series.Score.SecondParticipantWins,
			WinnerPath: match.WinnerPath, LoserPath: match.LoserPath,
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	return json.Marshal(document)
}
