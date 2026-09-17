package execution

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

const (
	maxDecisionEvidenceItems = 128
	maxDecisionEvidenceRunes = 512
)

type CommandScope = operationusecase.CommandScope
type PairingCommand = pairingusecase.PairingCommand
type PairingMode = pairingusecase.PairingMode
type PairingAuthority = pairingusecase.PairingAuthority
type PairingPlan = pairingusecase.PairingPlan
type SwissStandingView = pairingusecase.SwissStandingView

type WaveAction string

const (
	WaveActionOpenReadyWindow WaveAction = "open_ready_window"
	WaveActionStart           WaveAction = "start"
	WaveActionPause           WaveAction = "pause"
	WaveActionResume          WaveAction = "resume"
	WaveActionComplete        WaveAction = "complete"
	WaveActionCancel          WaveAction = "cancel"
)

type WaveCommand struct {
	CommandScope

	WaveID                     uuid.UUID
	ExpectedProjectionRevision int64
	Action                     WaveAction
	Confirmed                  bool
	Reason                     string
}

type SwissPairingEvidenceView struct {
	ID               uuid.UUID
	Purpose          string
	AlgorithmVersion string
	NormalizedInputs []string
	Result           []string
	ReplayDigest     string
	OwnerID          uuid.UUID
	DecidedAt        time.Time
}

type SwissPairingView struct {
	ID                  uuid.UUID
	RoundID             uuid.UUID
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	EvidenceID          uuid.UUID
	Repeated            bool
	OverrideActorID     *uuid.UUID
	OverrideReason      *string
}

type SwissByeView struct {
	ID            uuid.UUID
	RoundID       uuid.UUID
	ParticipantID uuid.UUID
	PointsAwarded int
	RevisionID    uuid.UUID
	EvidenceID    uuid.UUID
}

type SwissRoundView struct {
	ID                   uuid.UUID
	TournamentID         uuid.UUID
	RoundNumber          int
	Revision             int64
	RosterParticipantIDs []uuid.UUID
	PairingEvidence      *SwissPairingEvidenceView
	Pairings             []SwissPairingView
	Bye                  *SwissByeView
	Standings            []SwissStandingView
	Locked               bool
	LockedAt             *time.Time
	StartedAt            *time.Time
	CompletedAt          *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type WaveView struct {
	Wave               domain.Wave
	Revision           int64
	ReadinessRevisions map[uuid.UUID]int64
	SeriesIDs          map[uuid.UUID]uuid.UUID
	ByeParticipantID   *uuid.UUID
}

type PairingPort interface {
	ConfigurePairings(ctx context.Context, command PairingCommand) (SwissRoundView, error)
}

type WavePort interface {
	ControlWave(ctx context.Context, command WaveCommand) (WaveView, error)
}

func validWaveCommand(command WaveCommand) bool {
	return validCommandScope(command.CommandScope) && command.WaveID != uuid.Nil &&
		command.ExpectedProjectionRevision >= 1 && command.Action.valid() && command.Confirmed &&
		validOptionalText(command.Reason, maxReasonRunes)
}

func (action WaveAction) valid() bool {
	switch action {
	case WaveActionOpenReadyWindow, WaveActionStart, WaveActionPause, WaveActionResume,
		WaveActionComplete, WaveActionCancel:
		return true
	default:
		return false
	}
}

func validSwissRoundView(view SwissRoundView, tournamentID uuid.UUID, roundNumber int) bool {
	if !validSwissRoundHeader(view, tournamentID, roundNumber) {
		return false
	}
	roster := idSet(view.RosterParticipantIDs)
	paired, valid := validSwissPairings(view, roster)
	if !valid || !validSwissEvidenceReference(view) || !validSwissBye(view, roster, paired) {
		return false
	}
	if len(paired) != len(roster) {
		return false
	}
	return validSwissStandings(view, roster) && validSwissRoundTimeline(view)
}

func validSwissRoundHeader(view SwissRoundView, tournamentID uuid.UUID, roundNumber int) bool {
	return view.ID != uuid.Nil && view.TournamentID == tournamentID && view.RoundNumber == roundNumber &&
		view.Revision >= 1 && validUniqueIDs(view.RosterParticipantIDs, domain.TournamentMinParticipants, domain.TournamentMaxParticipants) &&
		view.Pairings != nil && len(view.Pairings) > 0 && len(view.Pairings) <= 8 && view.Standings != nil &&
		len(view.Standings) == len(view.RosterParticipantIDs) && domain.IsValidServerTime(view.CreatedAt) &&
		domain.IsValidServerTime(view.UpdatedAt) && !view.UpdatedAt.Before(view.CreatedAt) &&
		view.Locked == (view.LockedAt != nil)
}

func validSwissPairings(view SwissRoundView, roster map[uuid.UUID]struct{}) (map[uuid.UUID]struct{}, bool) {
	pairingIDs := make(map[uuid.UUID]struct{}, len(view.Pairings))
	paired := make(map[uuid.UUID]struct{}, len(view.Pairings)*2+1)
	for _, pairing := range view.Pairings {
		if !validSwissPairing(pairing, view.ID) || !containsID(roster, pairing.FirstParticipantID) ||
			!containsID(roster, pairing.SecondParticipantID) || !addUniqueID(pairingIDs, pairing.ID) ||
			!addUniqueID(paired, pairing.FirstParticipantID) || !addUniqueID(paired, pairing.SecondParticipantID) {
			return nil, false
		}
	}
	return paired, true
}

func validSwissPairing(pairing SwissPairingView, roundID uuid.UUID) bool {
	return pairing.ID != uuid.Nil && pairing.RoundID == roundID && pairing.FirstParticipantID != uuid.Nil &&
		pairing.SecondParticipantID != uuid.Nil && pairing.FirstParticipantID != pairing.SecondParticipantID &&
		pairing.EvidenceID != uuid.Nil && (pairing.OverrideActorID == nil || *pairing.OverrideActorID != uuid.Nil) &&
		(pairing.OverrideReason == nil || validText(*pairing.OverrideReason, maxReasonRunes)) &&
		(pairing.OverrideActorID == nil) == (pairing.OverrideReason == nil) &&
		pairing.Repeated == (pairing.OverrideActorID != nil)
}

func validSwissEvidenceReference(view SwissRoundView) bool {
	if view.PairingEvidence == nil {
		return true
	}
	if !validSwissPairingEvidence(*view.PairingEvidence, view.ID) {
		return false
	}
	for _, pairing := range view.Pairings {
		if pairing.EvidenceID != view.PairingEvidence.ID {
			return false
		}
	}
	return true
}

func validSwissBye(view SwissRoundView, roster, paired map[uuid.UUID]struct{}) bool {
	if view.Bye == nil {
		return true
	}
	bye := view.Bye
	if bye.ID == uuid.Nil || bye.RoundID != view.ID || bye.ParticipantID == uuid.Nil ||
		bye.PointsAwarded != 1 || bye.RevisionID == uuid.Nil || bye.EvidenceID == uuid.Nil ||
		!containsID(roster, bye.ParticipantID) {
		return false
	}
	return addUniqueID(paired, bye.ParticipantID)
}

func validSwissStandings(view SwissRoundView, roster map[uuid.UUID]struct{}) bool {
	standingParticipants := make(map[uuid.UUID]struct{}, len(view.Standings))
	positions := make(map[int]struct{}, len(view.Standings))
	seeds := make(map[int]struct{}, len(view.Standings))
	for _, standing := range view.Standings {
		if !validSwissStanding(standing, len(view.Standings)) || !containsID(roster, standing.ParticipantID) ||
			!addUniqueID(standingParticipants, standing.ParticipantID) || !addUniqueInt(positions, standing.Position) ||
			!addUniqueInt(seeds, standing.StableSeed) {
			return false
		}
	}
	return true
}

func validSwissRoundTimeline(view SwissRoundView) bool {
	if view.StartedAt != nil && !view.Locked || view.CompletedAt != nil && view.StartedAt == nil {
		return false
	}
	for _, value := range []*time.Time{view.LockedAt, view.StartedAt, view.CompletedAt} {
		if !validEventTime(value, view.CreatedAt, view.UpdatedAt) {
			return false
		}
	}
	return view.StartedAt == nil || view.CompletedAt == nil || !view.CompletedAt.Before(*view.StartedAt)
}

func validSwissPairingEvidence(view SwissPairingEvidenceView, roundID uuid.UUID) bool {
	if view.ID == uuid.Nil || view.Purpose != string(domain.DecisionPurposePairing) ||
		view.AlgorithmVersion != domain.DecisionAlgorithmV1 || view.OwnerID != roundID ||
		!domain.IsValidServerTime(view.DecidedAt) || !validSHA256Hex(view.ReplayDigest) ||
		!validDecisionEvidenceStrings(view.NormalizedInputs) || !validDecisionEvidenceStrings(view.Result) ||
		len(view.NormalizedInputs) != len(view.Result) {
		return false
	}
	normalized := append([]string(nil), view.NormalizedInputs...)
	if !slices.IsSorted(normalized) {
		return false
	}
	result := append([]string(nil), view.Result...)
	slices.Sort(result)
	return slices.Equal(normalized, result)
}

func validDecisionEvidenceStrings(values []string) bool {
	if len(values) == 0 || len(values) > maxDecisionEvidenceItems {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validText(value, maxDecisionEvidenceRunes) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validWaveView(view WaveView, tournamentID, waveID uuid.UUID) bool {
	if view.Revision < 1 || view.Wave.ID != waveID || view.Wave.TournamentID != tournamentID ||
		view.Wave.Validate() != nil || view.ReadinessRevisions == nil || view.SeriesIDs == nil ||
		len(view.ReadinessRevisions) != len(view.Wave.Members) {
		return false
	}
	byeID, hasBye := waveByeParticipant(view)
	seriesMemberCount := make(map[uuid.UUID]int, len(view.SeriesIDs))
	for _, member := range view.Wave.Members {
		if view.ReadinessRevisions[member.ParticipantID] < 1 {
			return false
		}
		seriesID, assigned := view.SeriesIDs[member.ParticipantID]
		if member.ParticipantID == byeID {
			if !hasBye || assigned {
				return false
			}
			continue
		}
		if !assigned || seriesID == uuid.Nil {
			return false
		}
		seriesMemberCount[seriesID]++
	}
	for _, count := range seriesMemberCount {
		if count != 2 {
			return false
		}
	}
	return true
}

func waveByeParticipant(view WaveView) (uuid.UUID, bool) {
	if view.ByeParticipantID == nil {
		return uuid.Nil, len(view.SeriesIDs) == len(view.Wave.Members)
	}
	byeID := *view.ByeParticipantID
	if byeID == uuid.Nil || len(view.SeriesIDs)+1 != len(view.Wave.Members) {
		return uuid.Nil, false
	}
	for _, member := range view.Wave.Members {
		if member.ParticipantID == byeID {
			return byeID, true
		}
	}
	return uuid.Nil, false
}

func ValidWaveCommand(command WaveCommand) bool {
	return validWaveCommand(command)
}

func ValidSwissRoundView(view SwissRoundView, tournamentID uuid.UUID, roundNumber int) bool {
	return validSwissRoundView(view, tournamentID, roundNumber)
}

func ValidSwissStandings(view SwissRoundView, roster map[uuid.UUID]struct{}) bool {
	return validSwissStandings(view, roster)
}

func ValidWaveView(view WaveView, tournamentID, waveID uuid.UUID) bool {
	return validWaveView(view, tournamentID, waveID)
}
