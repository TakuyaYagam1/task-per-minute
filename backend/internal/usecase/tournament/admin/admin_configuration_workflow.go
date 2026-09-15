package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

const (
	configurationOperationUpdate     = "update_configuration"
	configurationOperationSeries     = "update_unstarted_series"
	configurationOperationSwissRound = "revise_swiss_round"
	configurationMaxReasonRunes      = 512
)

var (
	// ErrCutoff is returned when an otherwise valid source is already locked,
	// started, consumed or disclosed. It also remains a domain conflict so
	// inbound callers can use the same conflict handling as other admin writes.
	ErrCutoff = errors.Join(domain.ErrConflict, errors.New("configuration edit cutoff"))

	configurationMutationNamespace = uuid.MustParse("4e06fae5-720b-4a3a-9c4b-9e9fa1f50ca9")
)

type ConfigurationQuery struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
}

type ConfigurationUpdateCommand struct {
	CommandScope

	ExpectedProjectionRevision    int64
	ExpectedConfigurationRevision int64
	Confirmed                     bool
	Reason                        string
	SwissDefault                  inbound.AdminConfigurationStageDefault
	SemifinalDefault              inbound.AdminConfigurationStageDefault
	UnlockIntents                 []inbound.AdminConfigurationUnlockIntent
}

type UnstartedSeriesUpdateCommand struct {
	CommandScope

	SeriesID                   uuid.UUID
	ExpectedProjectionRevision int64
	ExpectedSeriesRevision     int64
	Confirmed                  bool
	Reason                     string
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	UnlockIntents              []inbound.AdminConfigurationUnlockIntent
}

type SwissRoundRevisionCommand struct {
	CommandScope

	RoundNumber                int
	ExpectedProjectionRevision int64
	ExpectedRoundRevision      int64
	Confirmed                  bool
	Reason                     string
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	ManualPairings             []inbound.AdminConfigurationParticipantPair
	ManualByeParticipantID     *uuid.UUID
	UnlockIntents              []inbound.AdminConfigurationUnlockIntent
}

type ConfigurationStageDefault = inbound.AdminConfigurationStageDefault

type ConfigurationReservation struct {
	ID               uuid.UUID
	OwnerID          uuid.UUID
	SourceRevisionID uuid.UUID
	Revision         int64
	Used             bool
	Disclosed        bool
	EvidenceDigest   [sha256.Size]byte
}

// ConfigurationArtifact is the repository's compact dependency record. It is
// intentionally independent from a database row and carries enough source
// evidence for the use case to fail closed before ExecuteMutation is called.
type ConfigurationArtifact struct {
	Kind                  string
	ID                    uuid.UUID
	Stage                 domain.TournamentStage
	Revision              int64
	PreviousRevisionID    uuid.UUID
	SourceContentRevision int64
	SourcePoolRevisionID  uuid.UUID
	SourcePoolRevision    int64
	SeriesID              uuid.UUID
	RoundNumber           int
	State                 string
	Locked                bool
	Started               bool
	Consumed              bool
	Disclosed             bool
	Reservations          []ConfigurationReservation
}

const (
	ConfigurationArtifactPlanned  = "planned"
	ConfigurationArtifactLocked   = "locked"
	ConfigurationArtifactActive   = "active"
	ConfigurationArtifactTerminal = "terminal"
)

type ConfigurationSeries struct {
	ID                     uuid.UUID
	FirstParticipantID     uuid.UUID
	SecondParticipantID    uuid.UUID
	Stage                  domain.TournamentStage
	RoundNumber            int
	Revision               int64
	Mode                   domain.CategoryMode
	Categories             []domain.Category
	CategoryPoolRevisionID uuid.UUID
	CategoryPoolRevision   int64
	State                  domain.SeriesState
	Locked                 bool
	Started                bool
	Consumed               bool
	Disclosed              bool
	Reservations           []ConfigurationReservation
}

type ConfigurationRound struct {
	ID               uuid.UUID
	Stage            domain.TournamentStage
	Number           int
	Revision         int64
	ParticipantIDs   []uuid.UUID
	SeriesIDs        []uuid.UUID
	Pairings         []inbound.AdminConfigurationParticipantPair
	ByeParticipantID *uuid.UUID
	ByeRevisionID    *uuid.UUID
	Locked           bool
	Started          bool
	Consumed         bool
	Disclosed        bool
	Reservations     []ConfigurationReservation
}

type ConfigurationAuthority struct {
	TournamentID         uuid.UUID
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	Configuration        domain.ContentConfiguration
	TournamentState      domain.TournamentState
	TournamentRevision   int64
	SwissDefault         ConfigurationStageDefault
	GoldenDefault        ConfigurationStageDefault
	SemifinalDefault     ConfigurationStageDefault
	FinalDefault         ConfigurationStageDefault
	Standings            []SwissStandingView
	Series               []ConfigurationSeries
	Rounds               []ConfigurationRound
	Artifacts            []ConfigurationArtifact
	UpdatedAt            time.Time
	Recorded             *ConfigurationCommandRecord
}

type ConfigurationLoadQuery struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}

type ConfigurationCommandRecord struct {
	CommandID                     uuid.UUID
	TournamentID                  uuid.UUID
	OperatorID                    uuid.UUID
	Operation                     string
	ExpectedProjectionRevision    int64
	ExpectedConfigurationRevision int64
	ExpectedSeriesRevision        int64
	ExpectedRoundRevision         int64
	RequestDigest                 [sha256.Size]byte
	Evidence                      inbound.AdminConfigurationMutationEvidence
	ExecutedAt                    time.Time
}

type ConfigurationMutation struct {
	Operation            string
	CommandID            uuid.UUID
	Authority            ConfigurationAuthority
	RequestDigest        [sha256.Size]byte
	NextConfiguration    domain.ContentConfiguration
	NextSwissDefault     ConfigurationStageDefault
	NextSemifinalDefault ConfigurationStageDefault
	Affected             []ConfigurationArtifact
	Superseded           []inbound.AdminConfigurationArtifactView
	Rebuilt              []ConfigurationArtifact
	UnlockIntents        []inbound.AdminConfigurationUnlockIntent
	SeriesChange         *ConfigurationSeriesChange
	RoundChange          *ConfigurationRoundChange
	Evidence             inbound.AdminConfigurationMutationEvidence
}

type ConfigurationSeriesChange struct {
	Previous ConfigurationSeries
	Next     ConfigurationSeries
}

type ConfigurationRoundChange struct {
	Previous           ConfigurationRound
	Next               ConfigurationRound
	Series             []ConfigurationSeriesChange
	PriorMeetingCounts map[swissusecase.PairKey]int
	Bye                *swissusecase.ByeSelection
}

type ConfigurationMutationResult struct {
	Evidence inbound.AdminConfigurationMutationEvidence
	Changed  bool
}

type TournamentConfigurationRepository interface {
	LoadConfiguration(ctx context.Context, query ConfigurationLoadQuery) (ConfigurationAuthority, error)
	ExecuteMutation(ctx context.Context, mutation ConfigurationMutation) (ConfigurationMutationResult, error)
}

type TournamentConfigurationWorkflow struct {
	repository TournamentConfigurationRepository
}

func NewTournamentConfigurationWorkflow(repository TournamentConfigurationRepository) *TournamentConfigurationWorkflow {
	return &TournamentConfigurationWorkflow{repository: repository}
}

var _ inbound.TournamentConfigurationUseCase = (*TournamentConfigurationWorkflow)(nil)

func (w *TournamentConfigurationWorkflow) GetTournamentConfiguration(
	ctx context.Context,
	query inbound.AdminTournamentConfigurationQuery,
) (inbound.AdminTournamentConfigurationView, error) {
	return w.getTournamentConfigurationInternal(ctx, ConfigurationQuery{
		Operator:     OperatorIdentity{ActorID: query.Operator.ActorID},
		TournamentID: query.TournamentID,
	})
}

func (w *TournamentConfigurationWorkflow) getTournamentConfigurationInternal(
	ctx context.Context,
	query ConfigurationQuery,
) (inbound.AdminTournamentConfigurationView, error) {
	if ctx == nil || !validConfigurationQuery(query) || w == nil || w.repository == nil {
		return inbound.AdminTournamentConfigurationView{}, domain.ErrValidation
	}
	authority, err := w.repository.LoadConfiguration(ctx, ConfigurationLoadQuery{
		Operator: query.Operator, TournamentID: query.TournamentID,
	})
	if err != nil {
		return inbound.AdminTournamentConfigurationView{}, err
	}
	if err := authority.Validate(query.TournamentID); err != nil {
		return inbound.AdminTournamentConfigurationView{}, err
	}
	return configurationView(authority), nil
}

func (w *TournamentConfigurationWorkflow) UpdateTournamentConfiguration(
	ctx context.Context,
	command inbound.AdminUpdateTournamentConfigurationCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	return w.updateTournamentConfigurationInternal(ctx, ConfigurationUpdateCommand{
		CommandScope: CommandScope{
			Operator: OperatorIdentity{ActorID: command.Operator.ActorID}, TournamentID: command.TournamentID, CommandID: command.CommandID,
		},
		ExpectedProjectionRevision: command.ExpectedProjectionRevision, ExpectedConfigurationRevision: command.ExpectedConfigurationRevision,
		Confirmed: command.Confirmed, Reason: command.Reason, SwissDefault: cloneStageDefault(command.SwissDefault),
		SemifinalDefault: cloneStageDefault(command.SemifinalDefault), UnlockIntents: cloneUnlockIntents(command.UnlockIntents),
	})
}

//nolint:gocyclo // The command boundary validates all revisions and editable dependencies before one mutation.
func (w *TournamentConfigurationWorkflow) updateTournamentConfigurationInternal(
	ctx context.Context,
	command ConfigurationUpdateCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	if ctx == nil || !validConfigurationUpdateCommand(command) || w == nil || w.repository == nil {
		return inbound.AdminConfigurationMutationEvidence{}, domain.ErrValidation
	}
	digest, err := configurationRequestDigest(configurationOperationUpdate, command)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	authority, err := w.loadForMutation(ctx, command.CommandScope)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.Validate(command.TournamentID); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if replay, ok, replayErr := replayConfigurationCommand(authority, command.CommandScope, configurationOperationUpdate, command.ExpectedProjectionRevision, command.ExpectedConfigurationRevision, 0, digest); ok {
		return replay, replayErr
	}
	if err := authority.MatchRevisions(command.ExpectedProjectionRevision, command.ExpectedConfigurationRevision); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.validateStageSelection(domain.TournamentStageSwiss, command.SwissDefault); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.validateStageSelection(domain.TournamentStageSemifinal, command.SemifinalDefault); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if sameStageDefault(authority.SwissDefault, command.SwissDefault) &&
		sameStageDefault(authority.SemifinalDefault, command.SemifinalDefault) {
		if len(command.UnlockIntents) != 0 {
			return inbound.AdminConfigurationMutationEvidence{}, domain.ErrValidation
		}
		return configurationEvidence(command.CommandScope, digest, authority, authority.Configuration.Revision, nil, nil, command.Reason, authority.UpdatedAt), nil
	}
	affected := authority.affectedForDefaults(command.SwissDefault, command.SemifinalDefault)
	if err := validateEditableArtifacts(affected); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := validateUnlockIntents(command.TournamentID, affected, command.UnlockIntents); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	next := cloneContentConfiguration(authority.Configuration)
	next.Revision++
	next.PlanRevisionID = uuid.Nil
	next.PreflightRevisionID = uuid.Nil
	setStageMode(&next, domain.TournamentStageSwiss, command.SwissDefault.Mode)
	setStageMode(&next, domain.TournamentStageSemifinal, command.SemifinalDefault.Mode)
	rebuilt, superseded := successorArtifacts(command.CommandID, affected)
	evidence := configurationEvidence(command.CommandScope, digest, authority, next.Revision, affected, superseded, command.Reason, authority.UpdatedAt)
	evidence.RebuiltArtifactIDs = artifactIDs(rebuilt)
	mutation := ConfigurationMutation{
		Operation: configurationOperationUpdate, CommandID: command.CommandID, Authority: authority,
		RequestDigest: digest, NextConfiguration: next,
		NextSwissDefault: cloneStageDefault(command.SwissDefault), NextSemifinalDefault: cloneStageDefault(command.SemifinalDefault),
		Affected: cloneArtifacts(affected), Superseded: cloneArtifactViews(superseded), Rebuilt: cloneArtifacts(rebuilt),
		UnlockIntents: cloneUnlockIntents(command.UnlockIntents), Evidence: evidence,
	}
	return w.execute(ctx, mutation)
}

func (w *TournamentConfigurationWorkflow) UpdateUnstartedSeries(
	ctx context.Context,
	command inbound.AdminUpdateUnstartedSeriesCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	return w.updateUnstartedSeriesInternal(ctx, UnstartedSeriesUpdateCommand{
		CommandScope: CommandScope{
			Operator: OperatorIdentity{ActorID: command.Operator.ActorID}, TournamentID: command.TournamentID, CommandID: command.CommandID,
		},
		SeriesID: command.SeriesID, ExpectedProjectionRevision: command.ExpectedProjectionRevision, ExpectedSeriesRevision: command.ExpectedSeriesRevision,
		Confirmed: command.Confirmed, Reason: command.Reason, CategoryMode: command.CategoryMode,
		Categories: append([]domain.Category(nil), command.Categories...), UnlockIntents: cloneUnlockIntents(command.UnlockIntents),
	})
}

//nolint:gocyclo // The command boundary keeps validation, replay and cutoff checks together.
func (w *TournamentConfigurationWorkflow) updateUnstartedSeriesInternal(
	ctx context.Context,
	command UnstartedSeriesUpdateCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	if ctx == nil || !validUnstartedSeriesCommand(command) || w == nil || w.repository == nil {
		return inbound.AdminConfigurationMutationEvidence{}, domain.ErrValidation
	}
	digest, err := configurationRequestDigest(configurationOperationSeries, command)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	authority, err := w.loadForMutation(ctx, command.CommandScope)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.Validate(command.TournamentID); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if replay, ok, replayErr := replayConfigurationCommand(authority, command.CommandScope, configurationOperationSeries, command.ExpectedProjectionRevision, 0, command.ExpectedSeriesRevision, digest); ok {
		return replay, replayErr
	}
	series, found := findSeries(authority.Series, command.SeriesID)
	if !found || authority.ProjectionRevision != command.ExpectedProjectionRevision || series.Revision != command.ExpectedSeriesRevision {
		return inbound.AdminConfigurationMutationEvidence{}, authority.conflict(command.ExpectedProjectionRevision, authority.Configuration.Revision)
	}
	if series.Stage == domain.TournamentStageFinal || !editableSeries(series) {
		return inbound.AdminConfigurationMutationEvidence{}, cutoffError("series is no longer editable")
	}
	if err := authority.validateStageSelection(series.Stage, inbound.AdminConfigurationStageDefault{Mode: command.CategoryMode, Categories: command.Categories}); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	reservations := series.Reservations
	if err := validateUnlockIntents(command.TournamentID, reservationsAsArtifacts(reservations, series.ID, series.Stage), command.UnlockIntents); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if series.Mode == command.CategoryMode && slices.Equal(series.Categories, canonicalCategories(command.Categories)) {
		return configurationEvidence(command.CommandScope, digest, authority, authority.Configuration.Revision, []ConfigurationArtifact{seriesArtifact(series)}, nil, command.Reason, authority.UpdatedAt), nil
	}
	nextSeries := series
	nextSeries.Revision++
	nextSeries.Mode = command.CategoryMode
	nextSeries.Categories = canonicalCategories(command.Categories)
	rebuilt, superseded := successorArtifacts(command.CommandID, []ConfigurationArtifact{seriesArtifact(series)})
	evidence := configurationEvidence(command.CommandScope, digest, authority, authority.Configuration.Revision, []ConfigurationArtifact{seriesArtifact(series)}, superseded, command.Reason, authority.UpdatedAt)
	evidence.RebuiltArtifactIDs = artifactIDs(rebuilt)
	mutation := ConfigurationMutation{
		Operation: configurationOperationSeries, CommandID: command.CommandID, Authority: authority, RequestDigest: digest,
		NextConfiguration: cloneContentConfiguration(authority.Configuration), Affected: []ConfigurationArtifact{seriesArtifact(series)},
		Superseded: cloneArtifactViews(superseded), Rebuilt: cloneArtifacts(rebuilt), UnlockIntents: cloneUnlockIntents(command.UnlockIntents),
		SeriesChange: &ConfigurationSeriesChange{Previous: cloneConfigurationSeries(series), Next: cloneConfigurationSeries(nextSeries)}, Evidence: evidence,
	}
	return w.execute(ctx, mutation)
}

func (w *TournamentConfigurationWorkflow) ReviseSwissRound(
	ctx context.Context,
	command inbound.AdminReviseSwissRoundCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	return w.reviseSwissRoundInternal(ctx, SwissRoundRevisionCommand{
		CommandScope: CommandScope{
			Operator: OperatorIdentity{ActorID: command.Operator.ActorID}, TournamentID: command.TournamentID, CommandID: command.CommandID,
		},
		RoundNumber: command.RoundNumber, ExpectedProjectionRevision: command.ExpectedProjectionRevision, ExpectedRoundRevision: command.ExpectedRoundRevision,
		Confirmed: command.Confirmed, Reason: command.Reason, CategoryMode: command.CategoryMode,
		Categories: append([]domain.Category(nil), command.Categories...), ManualPairings: clonePairings(command.ManualPairings),
		ManualByeParticipantID: cloneUUID(command.ManualByeParticipantID), UnlockIntents: cloneUnlockIntents(command.UnlockIntents),
	})
}

//nolint:gocyclo // The round command validates pairings and every dependent Series before one mutation.
func (w *TournamentConfigurationWorkflow) reviseSwissRoundInternal(
	ctx context.Context,
	command SwissRoundRevisionCommand,
) (inbound.AdminConfigurationMutationEvidence, error) {
	if ctx == nil || !validSwissRoundRevisionCommand(command) || w == nil || w.repository == nil {
		return inbound.AdminConfigurationMutationEvidence{}, domain.ErrValidation
	}
	digest, err := configurationRequestDigest(configurationOperationSwissRound, command)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	authority, err := w.loadForMutation(ctx, command.CommandScope)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.Validate(command.TournamentID); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if replay, ok, replayErr := replayConfigurationCommand(authority, command.CommandScope, configurationOperationSwissRound, command.ExpectedProjectionRevision, 0, command.ExpectedRoundRevision, digest); ok {
		return replay, replayErr
	}
	round, found := findRound(authority.Rounds, command.RoundNumber)
	if !found || authority.ProjectionRevision != command.ExpectedProjectionRevision || round.Revision != command.ExpectedRoundRevision {
		return inbound.AdminConfigurationMutationEvidence{}, authority.conflict(command.ExpectedProjectionRevision, authority.Configuration.Revision)
	}
	if round.Stage != domain.TournamentStageSwiss || !editableRound(round) {
		return inbound.AdminConfigurationMutationEvidence{}, configurationCutoffConflict(
			authority, command.ExpectedProjectionRevision, command.ExpectedRoundRevision,
			"Swiss round is no longer editable",
		)
	}
	if err := validateManualRound(command, round); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	byeSelection, err := authority.selectSwissBye(command, round)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	priorMeetingCounts, err := authority.PriorMeetingCountsBeforeRound(round.Number)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := validateNoPriorSwissMeetings(command.ManualPairings, priorMeetingCounts); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if err := authority.validateStageSelection(domain.TournamentStageSwiss, inbound.AdminConfigurationStageDefault{Mode: command.CategoryMode, Categories: command.Categories}); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	affected := make([]ConfigurationArtifact, 0, len(round.SeriesIDs)+1)
	affected = append(affected, roundArtifact(round))
	for _, seriesID := range round.SeriesIDs {
		series, ok := findSeries(authority.Series, seriesID)
		if !ok {
			return inbound.AdminConfigurationMutationEvidence{}, domain.ErrConflict
		}
		if !editableSeries(series) {
			return inbound.AdminConfigurationMutationEvidence{}, configurationCutoffConflict(
				authority, command.ExpectedProjectionRevision, command.ExpectedRoundRevision,
				"Swiss series is no longer editable",
			)
		}
		affected = append(affected, seriesArtifact(series))
	}
	if err := validateUnlockIntents(command.TournamentID, affected, command.UnlockIntents); err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	nextRound := cloneConfigurationRound(round)
	nextRound.Revision++
	nextRound.Pairings = canonicalPairings(command.ManualPairings)
	nextRound.ByeParticipantID = nil
	nextRound.ByeRevisionID = nil
	if byeSelection != nil {
		participantID := byeSelection.ParticipantID
		nextRound.ByeParticipantID = &participantID
		evidenceID := byeSelection.Evidence.ID
		nextRound.ByeRevisionID = &evidenceID
	}
	rebuilt, superseded := successorArtifacts(command.CommandID, affected)
	evidence := configurationEvidence(command.CommandScope, digest, authority, authority.Configuration.Revision, affected, superseded, command.Reason, authority.UpdatedAt)
	evidence.RebuiltArtifactIDs = artifactIDs(rebuilt)
	mutation := ConfigurationMutation{
		Operation: configurationOperationSwissRound, CommandID: command.CommandID, Authority: authority, RequestDigest: digest,
		NextConfiguration: cloneContentConfiguration(authority.Configuration), Affected: cloneArtifacts(affected),
		Superseded: cloneArtifactViews(superseded), Rebuilt: cloneArtifacts(rebuilt), UnlockIntents: cloneUnlockIntents(command.UnlockIntents),
		RoundChange: &ConfigurationRoundChange{Previous: cloneConfigurationRound(round), Next: nextRound, PriorMeetingCounts: clonePriorMeetingCounts(priorMeetingCounts), Bye: cloneByeSelection(byeSelection)}, Evidence: evidence,
	}
	if len(round.SeriesIDs) != len(command.ManualPairings) {
		return inbound.AdminConfigurationMutationEvidence{}, domain.ErrConflict
	}
	for index, seriesID := range round.SeriesIDs {
		series, _ := findSeries(authority.Series, seriesID)
		nextSeries := cloneConfigurationSeries(series)
		nextSeries.Revision++
		nextSeries.FirstParticipantID = nextRound.Pairings[index].FirstParticipantID
		nextSeries.SecondParticipantID = nextRound.Pairings[index].SecondParticipantID
		nextSeries.Mode = command.CategoryMode
		nextSeries.Categories = canonicalCategories(command.Categories)
		mutation.RoundChange.Series = append(mutation.RoundChange.Series, ConfigurationSeriesChange{Previous: cloneConfigurationSeries(series), Next: nextSeries})
	}
	return w.execute(ctx, mutation)
}

func (w *TournamentConfigurationWorkflow) loadForMutation(ctx context.Context, scope CommandScope) (ConfigurationAuthority, error) {
	if !validCommandScope(scope) {
		return ConfigurationAuthority{}, domain.ErrValidation
	}
	return w.repository.LoadConfiguration(ctx, ConfigurationLoadQuery(scope))
}

func (w *TournamentConfigurationWorkflow) execute(ctx context.Context, mutation ConfigurationMutation) (inbound.AdminConfigurationMutationEvidence, error) {
	result, err := w.repository.ExecuteMutation(ctx, mutation)
	if err != nil {
		return inbound.AdminConfigurationMutationEvidence{}, err
	}
	if !evidenceMatchesMutation(result.Evidence, mutation) {
		return inbound.AdminConfigurationMutationEvidence{}, domain.ErrInternal
	}
	return cloneEvidence(result.Evidence), nil
}

func (a ConfigurationAuthority) MatchRevisions(expectedProjection, expectedConfiguration int64) error {
	if a.ProjectionRevision != expectedProjection || a.Configuration.Revision != expectedConfiguration {
		return a.conflict(expectedProjection, expectedConfiguration)
	}
	return nil
}

func (a ConfigurationAuthority) conflict(expectedProjection, expectedConfiguration int64) error {
	return &ConfigurationRevisionConflictError{
		ExpectedProjectionRevision:    expectedProjection,
		CurrentProjectionRevision:     a.ProjectionRevision,
		ExpectedConfigurationRevision: expectedConfiguration,
		CurrentConfigurationRevision:  a.Configuration.Revision,
		CurrentState:                  a.TournamentState,
	}
}

type ConfigurationRevisionConflictError struct {
	ExpectedProjectionRevision    int64
	CurrentProjectionRevision     int64
	ExpectedConfigurationRevision int64
	CurrentConfigurationRevision  int64
	CurrentState                  domain.TournamentState
}

func (e *ConfigurationRevisionConflictError) Error() string {
	return "tournament configuration revision conflict"
}

func (e *ConfigurationRevisionConflictError) Unwrap() error { return domain.ErrConflict }

func (e *ConfigurationRevisionConflictError) As(target any) bool {
	conflict, ok := target.(**inbound.AdminRevisionConflictError)
	if !ok {
		return false
	}
	*conflict = &inbound.AdminRevisionConflictError{
		ExpectedRevision: e.ExpectedProjectionRevision,
		CurrentRevision:  e.CurrentProjectionRevision,
		CurrentState:     e.CurrentState,
	}
	return true
}

func cutoffError(reason string) error {
	return fmt.Errorf("%w: %s", ErrCutoff, reason)
}

func configurationCutoffConflict(
	authority ConfigurationAuthority,
	expectedProjectionRevision int64,
	expectedConfigurationRevision int64,
	reason string,
) error {
	return fmt.Errorf(
		"%w: %w",
		authority.conflict(expectedProjectionRevision, expectedConfigurationRevision),
		cutoffError(reason),
	)
}

//nolint:gocyclo // Authority validation intentionally checks the complete cross-artifact snapshot.
func (a ConfigurationAuthority) Validate(tournamentID uuid.UUID) error {
	if tournamentID == uuid.Nil || a.TournamentID != tournamentID || a.ProjectionRevisionID == uuid.Nil ||
		a.ProjectionRevision < 1 || a.Configuration.TournamentID != tournamentID || a.Configuration.Revision < 1 ||
		!a.TournamentState.IsValid() || a.TournamentRevision < 1 {
		return fmt.Errorf("configuration authority identity is invalid: %w", domain.ErrInternal)
	}
	if err := a.Configuration.Validate(); err != nil {
		return fmt.Errorf("configuration authority content is invalid: %w", err)
	}
	if err := a.validateStageSelection(domain.TournamentStageSwiss, a.SwissDefault); err != nil {
		return fmt.Errorf("configuration authority Swiss default is invalid: %w", err)
	}
	if err := a.validateStageSelection(domain.TournamentStageGolden, a.GoldenDefault); err != nil {
		return fmt.Errorf("configuration authority Golden default is invalid: %w", err)
	}
	if err := a.validateStageSelection(domain.TournamentStageSemifinal, a.SemifinalDefault); err != nil {
		return fmt.Errorf("configuration authority semifinal default is invalid: %w", err)
	}
	if err := a.validateFinalDefault(); err != nil {
		return fmt.Errorf("configuration authority final default is invalid: %w", err)
	}
	seriesSeen := make(map[uuid.UUID]struct{}, len(a.Series))
	for _, series := range a.Series {
		if series.ID == uuid.Nil || series.Revision < 1 || !series.Stage.IsValid() || !series.Mode.IsValid() ||
			!series.State.IsValid() || !validUniqueCategories(series.Categories) || !addConfigurationIdentity(seriesSeen, series.ID) {
			return domain.ErrInternal
		}
		if err := a.validateStageSelection(series.Stage, ConfigurationStageDefault{Mode: series.Mode, Categories: series.Categories}); err != nil {
			return domain.ErrInternal
		}
		if series.Stage == domain.TournamentStageFinal && series.Mode != domain.CategoryModeDraft {
			return domain.ErrInternal
		}
	}
	roundSeen := make(map[uuid.UUID]struct{}, len(a.Rounds))
	for _, round := range a.Rounds {
		if round.ID == uuid.Nil || round.Number < 1 || round.Revision < 1 || !round.Stage.IsValid() ||
			!addConfigurationIdentity(roundSeen, round.ID) || !validUniqueIDs(round.ParticipantIDs, 1, len(round.ParticipantIDs)) {
			return domain.ErrInternal
		}
		if (round.ByeParticipantID == nil) != (round.ByeRevisionID == nil) ||
			(round.ByeParticipantID != nil && (*round.ByeParticipantID == uuid.Nil || *round.ByeRevisionID == uuid.Nil)) {
			return domain.ErrInternal
		}
	}
	artifactSeen := make(map[uuid.UUID]struct{}, len(a.Artifacts))
	for _, artifact := range a.Artifacts {
		if artifact.ID == uuid.Nil || artifact.Kind == "" || !artifact.Stage.IsValid() || !addConfigurationIdentity(artifactSeen, artifact.ID) {
			return domain.ErrInternal
		}
	}
	return nil
}

func (a ConfigurationAuthority) validateFinalDefault() error {
	pools := a.configurationPools()
	var pool domain.CategoryPoolRevision
	for _, candidate := range pools {
		if candidate.Format == domain.SeriesFormatBO3 {
			pool = candidate
		}
	}
	if pool.ID == uuid.Nil || a.FinalDefault.Mode != domain.CategoryModeDraft || !slices.Equal(canonicalCategories(a.FinalDefault.Categories), canonicalCategories(pool.Categories)) {
		return domain.ErrInvalidContentConfiguration
	}
	return nil
}

func (a ConfigurationAuthority) validateStageSelection(stage domain.TournamentStage, selection ConfigurationStageDefault) error {
	if !stage.IsValid() || !selection.Mode.IsValid() {
		return domain.ErrValidation
	}
	pool, ok := a.poolForStage(stage)
	if !ok {
		return domain.ErrInternal
	}
	categories := canonicalCategories(selection.Categories)
	if selection.Mode == domain.CategoryModeDraft {
		if len(categories) != len(pool.Categories) {
			return domain.ErrValidation
		}
	} else if len(categories) != 1 {
		return domain.ErrValidation
	}
	allowed := make(map[domain.Category]struct{}, len(pool.Categories))
	if selection.CategoryPoolRevisionID != uuid.Nil && selection.CategoryPoolRevisionID != pool.ID {
		return domain.ErrValidation
	}
	if selection.CategoryPoolRevision != 0 && selection.CategoryPoolRevision != pool.Revision {
		return domain.ErrValidation
	}
	for _, category := range pool.Categories {
		allowed[category] = struct{}{}
	}
	for _, category := range categories {
		if _, exists := allowed[category]; !exists {
			return domain.ErrValidation
		}
	}
	return nil
}

func (a ConfigurationAuthority) poolForStage(stage domain.TournamentStage) (domain.CategoryPoolRevision, bool) {
	var poolID uuid.UUID
	for _, item := range a.Configuration.StageDefaults {
		if item.Stage == stage {
			poolID = item.CategoryPoolRevisionID
			break
		}
	}
	for _, pool := range a.Configuration.CategoryPools {
		if pool.ID == poolID {
			return pool, true
		}
	}
	return domain.CategoryPoolRevision{}, false
}

func (a ConfigurationAuthority) configurationPools() []domain.CategoryPoolRevision {
	return append([]domain.CategoryPoolRevision(nil), a.Configuration.CategoryPools...)
}

func (a ConfigurationAuthority) affectedForDefaults(swiss, semifinal ConfigurationStageDefault) []ConfigurationArtifact {
	changedStages := make(map[domain.TournamentStage]struct{}, 2)
	if !sameStageDefault(a.SwissDefault, swiss) {
		changedStages[domain.TournamentStageSwiss] = struct{}{}
	}
	if !sameStageDefault(a.SemifinalDefault, semifinal) {
		changedStages[domain.TournamentStageSemifinal] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{})
	result := make([]ConfigurationArtifact, 0)
	for _, artifact := range a.Artifacts {
		if _, changed := changedStages[artifact.Stage]; changed {
			if _, duplicate := seen[artifact.ID]; !duplicate {
				seen[artifact.ID] = struct{}{}
				result = append(result, cloneConfigurationArtifact(artifact))
			}
		}
	}
	for _, series := range a.Series {
		if _, changed := changedStages[series.Stage]; changed {
			if _, duplicate := seen[series.ID]; !duplicate {
				seen[series.ID] = struct{}{}
				result = append(result, seriesArtifact(series))
			}
		}
	}
	for _, round := range a.Rounds {
		if _, changed := changedStages[round.Stage]; changed {
			if _, duplicate := seen[round.ID]; !duplicate {
				seen[round.ID] = struct{}{}
				result = append(result, roundArtifact(round))
			}
		}
	}
	return result
}

func validConfigurationQuery(query ConfigurationQuery) bool {
	return query.Operator.ActorID != uuid.Nil && query.TournamentID != uuid.Nil
}

func validConfigurationUpdateCommand(command ConfigurationUpdateCommand) bool {
	return validCommandScope(command.CommandScope) && command.ExpectedProjectionRevision >= 1 &&
		command.ExpectedConfigurationRevision >= 1 && command.Confirmed && validConfigurationReason(command.Reason)
}

func validUnstartedSeriesCommand(command UnstartedSeriesUpdateCommand) bool {
	return validCommandScope(command.CommandScope) && command.SeriesID != uuid.Nil && command.ExpectedProjectionRevision >= 1 &&
		command.ExpectedSeriesRevision >= 1 && command.Confirmed && validConfigurationReason(command.Reason) && command.CategoryMode.IsValid()
}

func validSwissRoundRevisionCommand(command SwissRoundRevisionCommand) bool {
	return validCommandScope(command.CommandScope) && command.RoundNumber >= 1 && command.ExpectedProjectionRevision >= 1 &&
		command.ExpectedRoundRevision >= 1 && command.Confirmed && validConfigurationReason(command.Reason) && command.CategoryMode.IsValid()
}

func validConfigurationReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	return reason != "" && utf8.RuneCountInString(reason) <= configurationMaxReasonRunes
}

func editableSeries(series ConfigurationSeries) bool {
	editableState := series.State == domain.SeriesStatePlanned || series.State == domain.SeriesStateLocked ||
		series.State == domain.SeriesStateDraft || series.State == domain.SeriesStateReady
	unlockable := !series.Locked || len(series.Reservations) > 0
	return editableState && unlockable && !series.Started && !series.Consumed && !series.Disclosed
}

func editableRound(round ConfigurationRound) bool {
	return !round.Locked && !round.Started && !round.Consumed && !round.Disclosed
}

func validateEditableArtifacts(artifacts []ConfigurationArtifact) error {
	for _, artifact := range artifacts {
		if artifact.State != "" && artifact.State != ConfigurationArtifactPlanned && artifact.State != ConfigurationArtifactLocked &&
			artifact.State != string(domain.SeriesStateDraft) && artifact.State != string(domain.SeriesStateReady) {
			return cutoffError("artifact is not planned")
		}
		if artifact.Started || artifact.Consumed || artifact.Disclosed {
			return cutoffError("artifact is no longer editable")
		}
		if artifact.Locked && len(artifact.Reservations) == 0 {
			return cutoffError("artifact has no unlock authority")
		}
	}
	return nil
}

//nolint:gocyclo // Every supplied intent is matched against immutable reservation evidence.
func validateUnlockIntents(tournamentID uuid.UUID, artifacts []ConfigurationArtifact, intents []inbound.AdminConfigurationUnlockIntent) error {
	required := make(map[uuid.UUID]ConfigurationReservation)
	for _, artifact := range artifacts {
		for _, reservation := range artifact.Reservations {
			if reservation.ID == uuid.Nil || reservation.Disclosed {
				return cutoffError("reservation is already disclosed")
			}
			required[reservation.ID] = reservation
		}
	}
	provided := make(map[uuid.UUID]struct{}, len(intents))
	for _, intent := range intents {
		if intent.ReservationID == uuid.Nil || intent.OwnerID == uuid.Nil || intent.SourceRevisionID == uuid.Nil || intent.ExpectedRevision < 1 ||
			intent.EvidenceDigest == ([sha256.Size]byte{}) || intent.BindingDigest == ([sha256.Size]byte{}) {
			return domain.ErrValidation
		}
		reservation, exists := required[intent.ReservationID]
		if !exists || reservation.OwnerID != intent.OwnerID || reservation.SourceRevisionID != intent.SourceRevisionID ||
			reservation.Revision != intent.ExpectedRevision || reservation.Used != intent.ExpectedUsed || reservation.Disclosed != intent.ExpectedDisclosed ||
			reservation.EvidenceDigest != intent.EvidenceDigest || configurationUnlockIntentDigest(tournamentID, intent) != intent.BindingDigest {
			return domain.ErrConflict
		}
		if _, duplicate := provided[intent.ReservationID]; duplicate {
			return domain.ErrValidation
		}
		provided[intent.ReservationID] = struct{}{}
	}
	if len(provided) != len(required) {
		return domain.ErrConflict
	}
	return nil
}

func (a ConfigurationAuthority) selectSwissBye(command SwissRoundRevisionCommand, round ConfigurationRound) (*swissusecase.ByeSelection, error) {
	if len(round.ParticipantIDs)%2 == 0 {
		return nil, nil
	}
	receivedByes, err := a.receivedSwissByesBeforeRound(round.Number)
	if err != nil {
		return nil, err
	}
	candidates, err := a.swissByeCandidates(round, receivedByes)
	if err != nil {
		return nil, err
	}
	requestedParticipantID := uuid.Nil
	if command.ManualByeParticipantID != nil {
		requestedParticipantID = *command.ManualByeParticipantID
	}
	evidenceID := configurationByeEvidenceID(command.CommandID, round.ID)
	selection, err := swissusecase.SelectManualBye(evidenceID, round.ID, candidates, requestedParticipantID, a.UpdatedAt)
	if err == nil {
		return &selection, nil
	}
	if !errors.Is(err, swissusecase.ErrManualByeNotEligible) {
		return nil, err
	}
	automatic, automaticErr := swissusecase.SelectBye(evidenceID, round.ID, candidates, a.UpdatedAt)
	if automaticErr == nil {
		return nil, &ManualByeMismatchError{
			RequestedParticipantID: requestedParticipantID,
			SelectedParticipantID:  automatic.ParticipantID,
			ExpectedRevision:       command.ExpectedProjectionRevision,
			CurrentRevision:        a.ProjectionRevision,
			CurrentState:           a.TournamentState,
		}
	}
	return nil, err
}

func (a ConfigurationAuthority) receivedSwissByesBeforeRound(roundNumber int) (map[uuid.UUID]bool, error) {
	if roundNumber < 1 {
		return nil, domain.ErrValidation
	}
	receivedByes := make(map[uuid.UUID]bool)
	seenRoundNumbers := make(map[int]struct{})
	for _, round := range a.Rounds {
		if round.Stage != domain.TournamentStageSwiss || round.Number >= roundNumber {
			continue
		}
		if _, duplicate := seenRoundNumbers[round.Number]; duplicate {
			return nil, fmt.Errorf("duplicate earlier Swiss round %d: %w", round.Number, domain.ErrInternal)
		}
		seenRoundNumbers[round.Number] = struct{}{}
		if round.ByeParticipantID == nil {
			continue
		}
		byeParticipantID := *round.ByeParticipantID
		if byeParticipantID == uuid.Nil {
			return nil, fmt.Errorf("invalid bye participant in earlier Swiss round %d: %w", round.Number, domain.ErrInternal)
		}
		known := false
		for _, participantID := range round.ParticipantIDs {
			if participantID == byeParticipantID {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("bye participant is absent from earlier Swiss round %d: %w", round.Number, domain.ErrInternal)
		}
		receivedByes[byeParticipantID] = true
	}
	return receivedByes, nil
}

func (a ConfigurationAuthority) swissByeCandidates(round ConfigurationRound, receivedByes map[uuid.UUID]bool) ([]swissusecase.ByeCandidate, error) {
	participants := make(map[uuid.UUID]struct{}, len(round.ParticipantIDs))
	for _, participantID := range round.ParticipantIDs {
		if participantID == uuid.Nil {
			return nil, domain.ErrInternal
		}
		if _, duplicate := participants[participantID]; duplicate {
			return nil, domain.ErrInternal
		}
		participants[participantID] = struct{}{}
	}
	if len(a.Standings) != len(participants) {
		return nil, fmt.Errorf("swiss standings do not cover the edited round: %w", domain.ErrInternal)
	}
	seen := make(map[uuid.UUID]struct{}, len(a.Standings))
	candidates := make([]swissusecase.ByeCandidate, 0, len(a.Standings))
	for _, standing := range a.Standings {
		if standing.ParticipantID == uuid.Nil {
			return nil, domain.ErrInternal
		}
		if _, exists := participants[standing.ParticipantID]; !exists {
			return nil, fmt.Errorf("swiss standing is outside the edited round: %w", domain.ErrInternal)
		}
		if _, duplicate := seen[standing.ParticipantID]; duplicate {
			return nil, fmt.Errorf("duplicate Swiss standing participant: %w", domain.ErrInternal)
		}
		seen[standing.ParticipantID] = struct{}{}
		candidates = append(candidates, swissusecase.ByeCandidate{
			ParticipantID:        standing.ParticipantID,
			Points:               standing.Points,
			ProvisionalBuchholz:  standing.Buchholz,
			HeadToHeadPoints:     standing.HeadToHeadPoints,
			HeadToHeadApplicable: standing.HeadToHeadApplied,
			EffectiveTime:        time.Duration(standing.EffectiveTimeMS) * time.Millisecond,
			ReceivedBye:          receivedByes[standing.ParticipantID],
		})
	}
	return candidates, nil
}

func configurationByeEvidenceID(commandID, roundID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(configurationMutationNamespace, []byte(commandID.String()+":swiss-bye:"+roundID.String()))
}

// PriorMeetingCountsBeforeRound returns the authoritative meeting counts for
// all Swiss pairs in rounds before roundNumber. The map is deliberately built
// from the retained round pairings, rather than from the round being revised,
// so a pre-start edit cannot erase history by replacing that round's children.
func (a ConfigurationAuthority) PriorMeetingCountsBeforeRound(roundNumber int) (map[swissusecase.PairKey]int, error) {
	if roundNumber < 1 {
		return nil, domain.ErrValidation
	}
	counts := make(map[swissusecase.PairKey]int)
	seenRoundNumbers := make(map[int]struct{})
	for _, round := range a.Rounds {
		if round.Stage != domain.TournamentStageSwiss || round.Number >= roundNumber {
			continue
		}
		if _, duplicate := seenRoundNumbers[round.Number]; duplicate {
			return nil, fmt.Errorf("duplicate earlier Swiss round %d: %w", round.Number, domain.ErrInternal)
		}
		seenRoundNumbers[round.Number] = struct{}{}
		pairings := round.Pairings
		if len(pairings) == 0 && len(round.SeriesIDs) > 0 {
			pairings = make([]inbound.AdminConfigurationParticipantPair, 0, len(round.SeriesIDs))
			for _, seriesID := range round.SeriesIDs {
				series, ok := findSeries(a.Series, seriesID)
				if !ok {
					return nil, fmt.Errorf("missing Series %s for earlier Swiss round %d: %w", seriesID, round.Number, domain.ErrInternal)
				}
				pairings = append(pairings, inbound.AdminConfigurationParticipantPair{
					FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
				})
			}
		}
		for _, pairing := range pairings {
			key, err := configurationPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)
			if err != nil {
				return nil, fmt.Errorf("invalid pairing in earlier Swiss round %d: %w", round.Number, domain.ErrInternal)
			}
			counts[key]++
		}
	}
	return counts, nil
}

func validateNoPriorSwissMeetings(
	pairings []inbound.AdminConfigurationParticipantPair,
	priorMeetingCounts map[swissusecase.PairKey]int,
) error {
	for _, pairing := range pairings {
		key, err := configurationPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)
		if err != nil {
			return domain.ErrValidation
		}
		if priorMeetingCounts[key] > 0 {
			return fmt.Errorf("swiss pairing occurred in an earlier round: %w", domain.ErrValidation)
		}
	}
	return nil
}

func configurationPairKey(first, second uuid.UUID) (swissusecase.PairKey, error) {
	if first == uuid.Nil || second == uuid.Nil || first == second {
		return swissusecase.PairKey{}, domain.ErrValidation
	}
	return swissusecase.NewPairKey(first, second), nil
}

//nolint:gocyclo // Manual pairing validation keeps participant coverage and bye rules in one pass.
func validateManualRound(command SwissRoundRevisionCommand, round ConfigurationRound) error {
	participants := append([]uuid.UUID(nil), round.ParticipantIDs...)
	if !validUniqueIDs(participants, 1, len(participants)) || len(participants) == 0 {
		return domain.ErrInternal
	}
	known := make(map[uuid.UUID]struct{}, len(participants))
	for _, participant := range participants {
		known[participant] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(participants))
	for _, pair := range command.ManualPairings {
		if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil || pair.FirstParticipantID == pair.SecondParticipantID {
			return domain.ErrValidation
		}
		if _, ok := known[pair.FirstParticipantID]; !ok {
			return domain.ErrValidation
		}
		if _, ok := known[pair.SecondParticipantID]; !ok {
			return domain.ErrValidation
		}
		if _, duplicate := seen[pair.FirstParticipantID]; duplicate {
			return domain.ErrValidation
		}
		if _, duplicate := seen[pair.SecondParticipantID]; duplicate {
			return domain.ErrValidation
		}
		seen[pair.FirstParticipantID] = struct{}{}
		seen[pair.SecondParticipantID] = struct{}{}
	}
	if len(participants)%2 == 0 {
		if command.ManualByeParticipantID != nil {
			return domain.ErrValidation
		}
	} else {
		if command.ManualByeParticipantID == nil || *command.ManualByeParticipantID == uuid.Nil {
			return domain.ErrValidation
		}
		if _, ok := known[*command.ManualByeParticipantID]; !ok {
			return domain.ErrValidation
		}
		if _, already := seen[*command.ManualByeParticipantID]; already {
			return domain.ErrValidation
		}
		seen[*command.ManualByeParticipantID] = struct{}{}
	}
	if len(seen) != len(participants) {
		return domain.ErrValidation
	}
	return nil
}

func replayConfigurationCommand(
	authority ConfigurationAuthority,
	scope CommandScope,
	operation string,
	expectedProjection, expectedConfiguration, expectedArtifact int64,
	digest [sha256.Size]byte,
) (inbound.AdminConfigurationMutationEvidence, bool, error) {
	if authority.Recorded == nil {
		return inbound.AdminConfigurationMutationEvidence{}, false, nil
	}
	record := authority.Recorded
	configurationMatches := expectedConfiguration == 0 || record.ExpectedConfigurationRevision == expectedConfiguration
	artifactMatches := expectedArtifact == 0 || record.ExpectedSeriesRevision == expectedArtifact || record.ExpectedRoundRevision == expectedArtifact
	if record.CommandID != scope.CommandID || record.TournamentID != scope.TournamentID || record.OperatorID != scope.Operator.ActorID ||
		record.Operation != operation || record.RequestDigest != digest || record.ExpectedProjectionRevision != expectedProjection ||
		!configurationMatches || !artifactMatches {
		return inbound.AdminConfigurationMutationEvidence{}, true, domain.ErrConflict
	}
	return cloneEvidence(record.Evidence), true, nil
}

func evidenceMatchesMutation(evidence inbound.AdminConfigurationMutationEvidence, mutation ConfigurationMutation) bool {
	return evidence.CommandID == mutation.CommandID && evidence.TournamentID == mutation.Authority.TournamentID &&
		evidence.ValidationDigest == mutation.RequestDigest
}

func configurationRequestDigest(operation string, command any) ([sha256.Size]byte, error) {
	canonical := canonicalConfigurationCommand(command)
	payload, err := json.Marshal(struct {
		Operation string `json:"operation"`
		Command   any    `json:"command"`
	}{Operation: operation, Command: canonical})
	if err != nil {
		return [sha256.Size]byte{}, domain.ErrInternal
	}
	return sha256.Sum256(payload), nil
}

func canonicalConfigurationCommand(command any) any {
	switch value := command.(type) {
	case ConfigurationUpdateCommand:
		value.Reason = strings.TrimSpace(value.Reason)
		value.SwissDefault = cloneStageDefault(value.SwissDefault)
		value.SemifinalDefault = cloneStageDefault(value.SemifinalDefault)
		value.UnlockIntents = canonicalUnlockIntents(value.UnlockIntents)
		return value
	case UnstartedSeriesUpdateCommand:
		value.Reason = strings.TrimSpace(value.Reason)
		value.Categories = canonicalCategories(value.Categories)
		value.UnlockIntents = canonicalUnlockIntents(value.UnlockIntents)
		return value
	case SwissRoundRevisionCommand:
		value.Reason = strings.TrimSpace(value.Reason)
		value.Categories = canonicalCategories(value.Categories)
		value.ManualPairings = canonicalPairings(value.ManualPairings)
		value.UnlockIntents = canonicalUnlockIntents(value.UnlockIntents)
		return value
	default:
		return command
	}
}

func configurationEvidence(scope CommandScope, digest [sha256.Size]byte, authority ConfigurationAuthority, nextRevision int64, affected []ConfigurationArtifact, superseded []inbound.AdminConfigurationArtifactView, reason string, requestedAt time.Time) inbound.AdminConfigurationMutationEvidence {
	return inbound.AdminConfigurationMutationEvidence{
		CommandID: scope.CommandID, TournamentID: scope.TournamentID, OperatorID: scope.Operator.ActorID,
		Reason: strings.TrimSpace(reason), RequestedAt: requestedAt, ValidationDigest: digest,
		PreviousConfigurationRevision: authority.Configuration.Revision, NextConfigurationRevision: nextRevision,
		AffectedArtifactIDs: artifactIDs(affected), SupersededArtifactIDs: artifactViewIDs(superseded),
		AffectedArtifacts: cloneArtifactViews(superseded),
	}
}

func configurationView(authority ConfigurationAuthority) inbound.AdminTournamentConfigurationView {
	view := inbound.AdminTournamentConfigurationView{
		TournamentID: authority.TournamentID, ProjectionRevisionID: authority.ProjectionRevisionID,
		ProjectionRevision: authority.ProjectionRevision, ConfigurationRevision: authority.Configuration.Revision,
		SwissDefault:     authority.effectiveStageDefault(domain.TournamentStageSwiss, authority.SwissDefault),
		GoldenDefault:    authority.effectiveStageDefault(domain.TournamentStageGolden, authority.GoldenDefault),
		SemifinalDefault: authority.effectiveStageDefault(domain.TournamentStageSemifinal, authority.SemifinalDefault),
		FinalDefault:     authority.effectiveStageDefault(domain.TournamentStageFinal, authority.FinalDefault),
		UpdatedAt:        authority.UpdatedAt,
	}
	view.CategoryPools = make([]inbound.AdminConfigurationCategoryPoolView, len(authority.Configuration.CategoryPools))
	for index, pool := range authority.Configuration.CategoryPools {
		view.CategoryPools[index] = inbound.AdminConfigurationCategoryPoolView{ID: pool.ID, Revision: pool.Revision, Format: pool.Format, Categories: canonicalCategories(pool.Categories)}
	}
	view.Series = make([]inbound.AdminConfigurationSeriesView, len(authority.Series))
	for index, series := range authority.Series {
		view.Series[index] = inbound.AdminConfigurationSeriesView{ID: series.ID, Stage: series.Stage, RoundNumber: series.RoundNumber, Revision: series.Revision, Mode: series.Mode, Categories: canonicalCategories(series.Categories), CategoryPoolRevisionID: series.CategoryPoolRevisionID, CategoryPoolRevision: series.CategoryPoolRevision, Locked: series.Locked, Started: series.Started, Consumed: series.Consumed, Disclosed: series.Disclosed, UnlockIntents: configurationUnlockIntents(authority.TournamentID, series.Reservations)}
	}
	view.Rounds = make([]inbound.AdminConfigurationRoundView, len(authority.Rounds))
	for index, round := range authority.Rounds {
		mode, categories := authority.SwissDefault.Mode, authority.SwissDefault.Categories
		if len(round.SeriesIDs) > 0 {
			if series, ok := findSeries(authority.Series, round.SeriesIDs[0]); ok {
				mode, categories = series.Mode, series.Categories
			}
		}
		view.Rounds[index] = inbound.AdminConfigurationRoundView{
			ID: round.ID, RoundNumber: round.Number, Revision: round.Revision,
			Mode: mode, Categories: canonicalCategories(categories), Pairings: clonePairings(round.Pairings),
			ByeParticipantID: cloneUUID(round.ByeParticipantID), Locked: round.Locked, Started: round.Started,
			Consumed: round.Consumed, Disclosed: round.Disclosed,
			UnlockIntents: configurationUnlockIntents(authority.TournamentID, round.Reservations),
		}
	}
	return view
}

func configurationUnlockIntents(tournamentID uuid.UUID, reservations []ConfigurationReservation) []inbound.AdminConfigurationUnlockIntent {
	result := make([]inbound.AdminConfigurationUnlockIntent, len(reservations))
	for index, reservation := range reservations {
		result[index] = inbound.AdminConfigurationUnlockIntent{
			ReservationID: reservation.ID, OwnerID: reservation.OwnerID, SourceRevisionID: reservation.SourceRevisionID,
			ExpectedRevision: reservation.Revision, ExpectedUsed: reservation.Used,
			ExpectedDisclosed: reservation.Disclosed, EvidenceDigest: reservation.EvidenceDigest,
		}
		result[index].BindingDigest = configurationUnlockIntentDigest(tournamentID, result[index])
	}
	return canonicalUnlockIntents(result)
}

//nolint:musttag // This canonical document is hashed and is not a wire representation.
func configurationUnlockIntentDigest(tournamentID uuid.UUID, intent inbound.AdminConfigurationUnlockIntent) [sha256.Size]byte {
	document := struct {
		ReservationID, TournamentID, OwnerID uuid.UUID
		SourceRevisionID                     uuid.UUID
		ExpectedRevision                     int64
		ExpectedUsed, ExpectedDisclosed      bool
		EvidenceDigest                       [sha256.Size]byte
	}{
		intent.ReservationID, tournamentID, intent.OwnerID, intent.SourceRevisionID,
		intent.ExpectedRevision, intent.ExpectedUsed, intent.ExpectedDisclosed, intent.EvidenceDigest,
	}
	payload, _ := json.Marshal(document)
	return sha256.Sum256(payload)
}

func (a ConfigurationAuthority) effectiveStageDefault(stage domain.TournamentStage, value ConfigurationStageDefault) ConfigurationStageDefault {
	value = cloneStageDefault(value)
	if pool, ok := a.poolForStage(stage); ok {
		value.CategoryPoolRevisionID = pool.ID
		value.CategoryPoolRevision = pool.Revision
	}
	return value
}

func cloneContentConfiguration(value domain.ContentConfiguration) domain.ContentConfiguration {
	clone := value
	clone.CategoryPools = make([]domain.CategoryPoolRevision, len(value.CategoryPools))
	for index, pool := range value.CategoryPools {
		clone.CategoryPools[index] = pool
		clone.CategoryPools[index].Categories = append([]domain.Category(nil), pool.Categories...)
	}
	clone.NormalPool = domain.CloneTaskPool(value.NormalPool)
	clone.GoldenPool = domain.CloneTaskPool(value.GoldenPool)
	clone.StageDefaults = append([]domain.StageContentDefault(nil), value.StageDefaults...)
	return clone
}

func setStageMode(configuration *domain.ContentConfiguration, stage domain.TournamentStage, mode domain.CategoryMode) {
	if configuration == nil {
		return
	}
	for index := range configuration.StageDefaults {
		if configuration.StageDefaults[index].Stage == stage {
			configuration.StageDefaults[index].CategoryMode = mode
		}
	}
}

func sameStageDefault(first, second ConfigurationStageDefault) bool {
	return first.Mode == second.Mode && slices.Equal(canonicalCategories(first.Categories), canonicalCategories(second.Categories))
}

func cloneStageDefault(value ConfigurationStageDefault) ConfigurationStageDefault {
	value.Categories = canonicalCategories(value.Categories)
	return value
}

func validUniqueCategories(values []domain.Category) bool {
	if len(values) == 0 {
		return false
	}
	canonical := canonicalCategories(values)
	for index, value := range canonical {
		if !value.IsValid() || index > 0 && canonical[index-1] == value {
			return false
		}
	}
	return true
}

func addConfigurationIdentity(seen map[uuid.UUID]struct{}, value uuid.UUID) bool {
	if value == uuid.Nil {
		return false
	}
	if _, exists := seen[value]; exists {
		return false
	}
	seen[value] = struct{}{}
	return true
}

func findSeries(values []ConfigurationSeries, id uuid.UUID) (ConfigurationSeries, bool) {
	for _, value := range values {
		if value.ID == id {
			return cloneConfigurationSeries(value), true
		}
	}
	return ConfigurationSeries{}, false
}

func findRound(values []ConfigurationRound, number int) (ConfigurationRound, bool) {
	for _, value := range values {
		if value.Number == number {
			return cloneConfigurationRound(value), true
		}
	}
	return ConfigurationRound{}, false
}

func seriesArtifact(series ConfigurationSeries) ConfigurationArtifact {
	return ConfigurationArtifact{Kind: "series", ID: series.ID, Stage: series.Stage, Revision: series.Revision, SeriesID: series.ID, RoundNumber: series.RoundNumber, State: string(series.State), Locked: series.Locked, Started: series.Started, Consumed: series.Consumed, Disclosed: series.Disclosed, Reservations: cloneReservations(series.Reservations)}
}

func roundArtifact(round ConfigurationRound) ConfigurationArtifact {
	return ConfigurationArtifact{Kind: "round", ID: round.ID, Stage: round.Stage, Revision: round.Revision, RoundNumber: round.Number, Locked: round.Locked, Started: round.Started, Consumed: round.Consumed, Disclosed: round.Disclosed, Reservations: cloneReservations(round.Reservations)}
}

func reservationsAsArtifacts(reservations []ConfigurationReservation, ownerID uuid.UUID, stage domain.TournamentStage) []ConfigurationArtifact {
	return []ConfigurationArtifact{{Kind: "series", ID: ownerID, Stage: stage, Reservations: cloneReservations(reservations)}}
}

func successorArtifacts(commandID uuid.UUID, affected []ConfigurationArtifact) ([]ConfigurationArtifact, []inbound.AdminConfigurationArtifactView) {
	rebuilt := make([]ConfigurationArtifact, 0, len(affected))
	superseded := make([]inbound.AdminConfigurationArtifactView, 0, len(affected))
	for _, artifact := range affected {
		successor := uuid.NewSHA1(configurationMutationNamespace, []byte(commandID.String()+":"+artifact.Kind+":"+artifact.ID.String()))
		rebuilt = append(rebuilt, ConfigurationArtifact{Kind: artifact.Kind, ID: successor, Stage: artifact.Stage, Revision: artifact.Revision + 1, PreviousRevisionID: artifact.ID})
		superseded = append(superseded, inbound.AdminConfigurationArtifactView{Kind: artifact.Kind, ID: artifact.ID, Stage: artifact.Stage, PreviousRevisionID: artifact.ID, SuccessorRevisionID: successor})
	}
	return rebuilt, superseded
}

func artifactIDs(values []ConfigurationArtifact) []uuid.UUID {
	ids := make([]uuid.UUID, len(values))
	for index, value := range values {
		ids[index] = value.ID
	}
	return ids
}

func artifactViewIDs(values []inbound.AdminConfigurationArtifactView) []uuid.UUID {
	ids := make([]uuid.UUID, len(values))
	for index, value := range values {
		ids[index] = value.ID
	}
	return ids
}

func cloneArtifacts(values []ConfigurationArtifact) []ConfigurationArtifact {
	cloned := make([]ConfigurationArtifact, len(values))
	for index, value := range values {
		cloned[index] = cloneConfigurationArtifact(value)
	}
	return cloned
}

func cloneConfigurationArtifact(value ConfigurationArtifact) ConfigurationArtifact {
	value.Reservations = cloneReservations(value.Reservations)
	return value
}

func cloneReservations(values []ConfigurationReservation) []ConfigurationReservation {
	return append([]ConfigurationReservation(nil), values...)
}

func cloneConfigurationSeries(value ConfigurationSeries) ConfigurationSeries {
	value.Categories = canonicalCategories(value.Categories)
	value.Reservations = cloneReservations(value.Reservations)
	return value
}

func cloneConfigurationRound(value ConfigurationRound) ConfigurationRound {
	value.ParticipantIDs = append([]uuid.UUID(nil), value.ParticipantIDs...)
	value.SeriesIDs = append([]uuid.UUID(nil), value.SeriesIDs...)
	value.Pairings = canonicalPairings(value.Pairings)
	value.ByeParticipantID = cloneUUID(value.ByeParticipantID)
	value.ByeRevisionID = cloneUUID(value.ByeRevisionID)
	value.Reservations = cloneReservations(value.Reservations)
	return value
}

func cloneByeSelection(value *swissusecase.ByeSelection) *swissusecase.ByeSelection {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Evidence.NormalizedInputs = append([]string(nil), value.Evidence.NormalizedInputs...)
	cloned.Evidence.Result = append([]string(nil), value.Evidence.Result...)
	return &cloned
}

func clonePriorMeetingCounts(value map[swissusecase.PairKey]int) map[swissusecase.PairKey]int {
	if value == nil {
		return nil
	}
	cloned := make(map[swissusecase.PairKey]int, len(value))
	for key, count := range value {
		cloned[key] = count
	}
	return cloned
}

func cloneArtifactViews(values []inbound.AdminConfigurationArtifactView) []inbound.AdminConfigurationArtifactView {
	return append([]inbound.AdminConfigurationArtifactView(nil), values...)
}

func cloneUnlockIntents(values []inbound.AdminConfigurationUnlockIntent) []inbound.AdminConfigurationUnlockIntent {
	return append([]inbound.AdminConfigurationUnlockIntent(nil), values...)
}

func canonicalUnlockIntents(values []inbound.AdminConfigurationUnlockIntent) []inbound.AdminConfigurationUnlockIntent {
	result := cloneUnlockIntents(values)
	slices.SortFunc(result, func(first, second inbound.AdminConfigurationUnlockIntent) int {
		return bytes.Compare(first.ReservationID[:], second.ReservationID[:])
	})
	return result
}

func canonicalPairings(values []inbound.AdminConfigurationParticipantPair) []inbound.AdminConfigurationParticipantPair {
	result := clonePairings(values)
	for index := range result {
		if bytes.Compare(result[index].FirstParticipantID[:], result[index].SecondParticipantID[:]) > 0 {
			result[index].FirstParticipantID, result[index].SecondParticipantID = result[index].SecondParticipantID, result[index].FirstParticipantID
		}
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].FirstParticipantID != result[second].FirstParticipantID {
			return bytes.Compare(result[first].FirstParticipantID[:], result[second].FirstParticipantID[:]) < 0
		}
		return bytes.Compare(result[first].SecondParticipantID[:], result[second].SecondParticipantID[:]) < 0
	})
	return result
}

func clonePairings(values []inbound.AdminConfigurationParticipantPair) []inbound.AdminConfigurationParticipantPair {
	return append([]inbound.AdminConfigurationParticipantPair(nil), values...)
}

func cloneEvidence(value inbound.AdminConfigurationMutationEvidence) inbound.AdminConfigurationMutationEvidence {
	value.AffectedArtifactIDs = append([]uuid.UUID(nil), value.AffectedArtifactIDs...)
	value.SupersededArtifactIDs = append([]uuid.UUID(nil), value.SupersededArtifactIDs...)
	value.RebuiltArtifactIDs = append([]uuid.UUID(nil), value.RebuiltArtifactIDs...)
	value.AffectedArtifacts = cloneArtifactViews(value.AffectedArtifacts)
	value.UnlockIntents = cloneUnlockIntents(value.UnlockIntents)
	return value
}
