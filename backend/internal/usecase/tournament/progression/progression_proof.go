package progression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func goldenProof(
	command Command,
	authority Authority,
	evidence SwissEvidence,
	ties []TieGroupRecord,
	now time.Time,
) (json.RawMessage, [sha256.Size]byte, error) {
	type tie struct {
		GroupID      uuid.UUID   `json:"group_id"`
		RevisionID   uuid.UUID   `json:"revision_id"`
		PositionFrom int         `json:"position_from"`
		PositionTo   int         `json:"position_to"`
		Participants []uuid.UUID `json:"participants"`
		Digest       string      `json:"digest"`
	}
	document := struct {
		Action             Action    `json:"action"`
		CommandID          uuid.UUID `json:"command_id"`
		TournamentID       uuid.UUID `json:"tournament_id"`
		RosterID           uuid.UUID `json:"roster_id"`
		SourceState        string    `json:"source_state"`
		SourceRevisionID   uuid.UUID `json:"source_revision_id"`
		SourceRevision     int64     `json:"source_revision"`
		SourceDigest       string    `json:"source_digest"`
		ExpectedRounds     int       `json:"expected_rounds"`
		TerminalRounds     int       `json:"terminal_rounds"`
		ExpectedWaves      int       `json:"expected_waves"`
		TerminalWaves      int       `json:"terminal_waves"`
		ExpectedSeries     int       `json:"expected_series"`
		TerminalSeries     int       `json:"terminal_series"`
		ImpactfulTieGroups []tie     `json:"impactful_tie_groups"`
		PlannedAt          time.Time `json:"planned_at"`
	}{
		Action: command.Action, CommandID: command.CommandID, TournamentID: command.TournamentID,
		RosterID: command.RosterID, SourceState: string(authority.Tournament.State),
		SourceRevisionID: evidence.Current.RevisionID,
		SourceRevision:   evidence.Current.Revision, SourceDigest: hex.EncodeToString(evidence.Current.Digest[:]),
		ExpectedRounds: evidence.ExpectedRounds, TerminalRounds: evidence.TerminalRounds,
		ExpectedWaves: evidence.ExpectedWaves, TerminalWaves: evidence.TerminalWaves,
		ExpectedSeries: evidence.ExpectedSeries, TerminalSeries: evidence.TerminalSeries,
		ImpactfulTieGroups: make([]tie, len(ties)), PlannedAt: now,
	}
	for index, group := range ties {
		document.ImpactfulTieGroups[index] = tie{
			GroupID: group.GroupID, RevisionID: group.RevisionID, PositionFrom: group.PositionFrom,
			PositionTo: group.PositionTo, Participants: append([]uuid.UUID(nil), group.Participants...),
			Digest: hex.EncodeToString(group.ProofDigest[:]),
		}
	}
	return marshalProof(document)
}

func playoffProof(
	command Command,
	authority Authority,
	evidence SwissEvidence,
	finalSwiss playoff.FinalSwissProjection,
	top4 playoff.Top4Snapshot,
	bracket playoff.SemifinalBracket,
	now time.Time,
) (json.RawMessage, [sha256.Size]byte, error) {
	if finalSwiss.Validate() != nil || top4.Validate() != nil || bracket.Validate() != nil || !bracket.Locked() {
		return nil, [sha256.Size]byte{}, progressionConflict("playoff plan is not immutable")
	}
	type top4Position struct {
		ParticipantID uuid.UUID `json:"participant_id"`
		Position      int       `json:"position"`
	}
	type semifinal struct {
		Position            int       `json:"position"`
		SeriesID            uuid.UUID `json:"series_id"`
		FirstParticipantID  uuid.UUID `json:"first_participant_id"`
		SecondParticipantID uuid.UUID `json:"second_participant_id"`
	}
	participants := top4.Participants()
	matches := bracket.Semifinals()
	if len(participants) != 4 || len(matches) != 2 {
		return nil, [sha256.Size]byte{}, progressionConflict("playoff plan has incomplete Top4 or semifinals")
	}
	document := struct {
		Action            Action         `json:"action"`
		CommandID         uuid.UUID      `json:"command_id"`
		TournamentID      uuid.UUID      `json:"tournament_id"`
		RosterID          uuid.UUID      `json:"roster_id"`
		SourceState       string         `json:"source_state"`
		SourceRevisionID  uuid.UUID      `json:"source_revision_id"`
		SourceRevision    int64          `json:"source_revision"`
		SourceDigest      string         `json:"source_digest"`
		ExpectedRounds    int            `json:"expected_rounds"`
		TerminalRounds    int            `json:"terminal_rounds"`
		ExpectedWaves     int            `json:"expected_waves"`
		TerminalWaves     int            `json:"terminal_waves"`
		ExpectedSeries    int            `json:"expected_series"`
		TerminalSeries    int            `json:"terminal_series"`
		Top4RevisionID    uuid.UUID      `json:"top4_revision_id"`
		BracketRevisionID uuid.UUID      `json:"bracket_revision_id"`
		Top4              []top4Position `json:"top4"`
		Semifinals        []semifinal    `json:"semifinals"`
		PlannedAt         time.Time      `json:"planned_at"`
	}{
		Action: command.Action, CommandID: command.CommandID, TournamentID: command.TournamentID,
		RosterID: command.RosterID, SourceState: string(authority.Tournament.State),
		SourceRevisionID: evidence.Current.RevisionID,
		SourceRevision:   evidence.Current.Revision, SourceDigest: hex.EncodeToString(evidence.Current.Digest[:]),
		ExpectedRounds: evidence.ExpectedRounds, TerminalRounds: evidence.TerminalRounds,
		ExpectedWaves: evidence.ExpectedWaves, TerminalWaves: evidence.TerminalWaves,
		ExpectedSeries: evidence.ExpectedSeries, TerminalSeries: evidence.TerminalSeries,
		Top4RevisionID:    top4.Projection().Revision().ID().UUID(),
		BracketRevisionID: bracket.Projection().Revision().ID().UUID(),
		Top4:              make([]top4Position, len(participants)), Semifinals: make([]semifinal, len(matches)), PlannedAt: now,
	}
	for index, participant := range participants {
		document.Top4[index] = top4Position{ParticipantID: participant.ParticipantID, Position: participant.Seed}
	}
	for index, match := range matches {
		document.Semifinals[index] = semifinal{
			Position: match.Position, SeriesID: match.Series.ID,
			FirstParticipantID: match.Series.FirstParticipantID, SecondParticipantID: match.Series.SecondParticipantID,
		}
	}
	return marshalProof(document)
}

func marshalProof(value any) (json.RawMessage, [sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) == 0 {
		return nil, [sha256.Size]byte{}, fmt.Errorf("%w: encode progression proof", domain.ErrInternal)
	}
	digest := sha256.Sum256(payload)
	return json.RawMessage(payload), digest, nil
}

func progressionConflict(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrConflict, fmt.Sprintf(format, arguments...))
}
