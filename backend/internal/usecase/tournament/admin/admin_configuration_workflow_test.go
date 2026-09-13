package admin

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestConfigurationUnlockIntentBindsReservationEvidence(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	reservation := ConfigurationReservation{
		ID: uuid.New(), OwnerID: uuid.New(), SourceRevisionID: uuid.New(), Revision: 3,
		Used: true, EvidenceDigest: sha256.Sum256([]byte("selection evidence")),
	}
	intent := configurationUnlockIntents(tournamentID, []ConfigurationReservation{reservation})[0]
	artifacts := []ConfigurationArtifact{{ID: uuid.New(), Reservations: []ConfigurationReservation{reservation}}}
	if err := validateUnlockIntents(tournamentID, artifacts, []inbound.AdminConfigurationUnlockIntent{intent}); err != nil {
		t.Fatalf("validateUnlockIntents() error = %v", err)
	}
	intent.BindingDigest[0] ^= 0xff
	if err := validateUnlockIntents(tournamentID, artifacts, []inbound.AdminConfigurationUnlockIntent{intent}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("forged binding error = %v, want conflict", err)
	}
}

type configurationRepositoryStub struct {
	authority ConfigurationAuthority
	result    ConfigurationMutationResult
	err       error
	calls     int
	last      ConfigurationMutation
}

func (r *configurationRepositoryStub) LoadConfiguration(_ context.Context, _ ConfigurationLoadQuery) (ConfigurationAuthority, error) {
	return r.authority, nil
}

func (r *configurationRepositoryStub) ExecuteMutation(_ context.Context, mutation ConfigurationMutation) (ConfigurationMutationResult, error) {
	r.calls++
	r.last = mutation
	if r.err != nil {
		return ConfigurationMutationResult{}, r.err
	}
	if r.result.Evidence.CommandID == uuid.Nil || r.result.Evidence.CommandID != mutation.CommandID {
		r.result = ConfigurationMutationResult{Evidence: mutation.Evidence, Changed: true}
	}
	return r.result, nil
}

func TestTournamentConfigurationWorkflowUpdatesOnlyAffectedPlannedArtifacts(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	artifactID := uuid.New()
	authority.Artifacts = []ConfigurationArtifact{{
		Kind: "swiss-round", ID: artifactID, Stage: domain.TournamentStageSwiss,
		Revision: 1, State: ConfigurationArtifactPlanned,
	}}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	command := inbound.AdminUpdateTournamentConfigurationCommand{
		Operator:                      inbound.AdminOperatorIdentity{ActorID: authorityOperatorID},
		TournamentID:                  authority.TournamentID,
		CommandID:                     uuid.New(),
		ExpectedProjectionRevision:    authority.ProjectionRevision,
		ExpectedConfigurationRevision: authority.Configuration.Revision,
		Confirmed:                     true,
		Reason:                        "replace Swiss category default",
		SwissDefault: inbound.AdminConfigurationStageDefault{
			Mode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryCrypto},
		},
		SemifinalDefault: authority.SemifinalDefault,
	}

	evidence, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
	if err != nil {
		t.Fatalf("UpdateTournamentConfiguration() error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("ExecuteMutation calls = %d, want 1", repository.calls)
	}
	if evidence.PreviousConfigurationRevision != 1 || evidence.NextConfigurationRevision != 2 {
		t.Fatalf("configuration revisions = %d -> %d, want 1 -> 2", evidence.PreviousConfigurationRevision, evidence.NextConfigurationRevision)
	}
	if len(evidence.AffectedArtifactIDs) != 1 || evidence.AffectedArtifactIDs[0] != artifactID {
		t.Fatalf("affected artifacts = %#v, want %s", evidence.AffectedArtifactIDs, artifactID)
	}
	if len(evidence.RebuiltArtifactIDs) != 1 || evidence.RebuiltArtifactIDs[0] == artifactID {
		t.Fatalf("rebuilt artifacts = %#v, want deterministic successor", evidence.RebuiltArtifactIDs)
	}
	if repository.last.NextConfiguration.Revision != 2 || repository.last.NextConfiguration.PlanRevisionID != uuid.Nil || repository.last.NextConfiguration.PreflightRevisionID != uuid.Nil {
		t.Fatalf("next configuration did not invalidate derived revisions: %#v", repository.last.NextConfiguration)
	}
}

func TestTournamentConfigurationWorkflowReturnsEffectiveDefaultsAndSeriesRevisions(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	seriesID := uuid.New()
	authority.Series = []ConfigurationSeries{{
		ID: seriesID, Stage: domain.TournamentStageSwiss, RoundNumber: 2, Revision: 4,
		Mode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryCrypto}, State: domain.SeriesStatePlanned,
	}}
	workflow := NewTournamentConfigurationWorkflow(&configurationRepositoryStub{authority: authority})
	view, err := workflow.GetTournamentConfiguration(t.Context(), inbound.AdminTournamentConfigurationQuery{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID,
	})
	if err != nil {
		t.Fatalf("GetTournamentConfiguration() error = %v", err)
	}
	if len(view.CategoryPools) != 2 || view.FinalDefault.Mode != domain.CategoryModeDraft || len(view.FinalDefault.Categories) != 5 {
		t.Fatalf("configuration view defaults = %#v, pools = %d", view.FinalDefault, len(view.CategoryPools))
	}
	if view.FinalDefault.CategoryPoolRevisionID == uuid.Nil || view.FinalDefault.CategoryPoolRevision != 1 {
		t.Fatalf("final default pool revision = %s/%d, want populated effective revision", view.FinalDefault.CategoryPoolRevisionID, view.FinalDefault.CategoryPoolRevision)
	}
	if len(view.Series) != 1 || view.Series[0].ID != seriesID || view.Series[0].Revision != 4 || view.Series[0].Mode != domain.CategoryModeAdmin || len(view.Series[0].Categories) != 1 {
		t.Fatalf("series view = %#v, want effective mode/categories/revision", view.Series)
	}
}

func TestTournamentConfigurationWorkflowRejectsCutoffBeforeMutation(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	authority.Artifacts = []ConfigurationArtifact{{
		Kind: "swiss-round", ID: uuid.New(), Stage: domain.TournamentStageSwiss,
		Revision: 1, State: ConfigurationArtifactLocked, Locked: true,
	}}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	command := configurationUpdateCommand(authority, uuid.New())

	_, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
	if !errors.Is(err, ErrCutoff) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("error = %v, want cutoff conflict", err)
	}
	if repository.calls != 0 {
		t.Fatalf("ExecuteMutation calls = %d, want 0", repository.calls)
	}
}

func TestTournamentConfigurationWorkflowRejectsStaleRevisionBeforeMutation(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	command := configurationUpdateCommand(authority, uuid.New())
	command.ExpectedProjectionRevision--

	_, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
	var conflict *ConfigurationRevisionConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("error = %v, want configuration conflict", err)
	}
	if repository.calls != 0 {
		t.Fatalf("ExecuteMutation calls = %d, want 0", repository.calls)
	}
}

func TestTournamentConfigurationWorkflowReplaysExactCommandAndRejectsDigestReuse(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	command := configurationUpdateCommand(authority, uuid.New())
	digest, err := configurationRequestDigest(configurationOperationUpdate, ConfigurationUpdateCommand{
		CommandScope:               CommandScope{Operator: OperatorIdentity{ActorID: command.Operator.ActorID}, TournamentID: command.TournamentID, CommandID: command.CommandID},
		ExpectedProjectionRevision: command.ExpectedProjectionRevision, ExpectedConfigurationRevision: command.ExpectedConfigurationRevision,
		Confirmed: command.Confirmed, Reason: command.Reason, SwissDefault: command.SwissDefault, SemifinalDefault: command.SemifinalDefault,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := inbound.AdminConfigurationMutationEvidence{CommandID: command.CommandID, TournamentID: command.TournamentID, OperatorID: command.Operator.ActorID, ValidationDigest: digest, Reason: command.Reason}
	authority.Recorded = &ConfigurationCommandRecord{CommandID: command.CommandID, TournamentID: command.TournamentID, OperatorID: command.Operator.ActorID, Operation: configurationOperationUpdate, ExpectedProjectionRevision: command.ExpectedProjectionRevision, ExpectedConfigurationRevision: command.ExpectedConfigurationRevision, RequestDigest: digest, Evidence: evidence}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)

	replayed, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
	if err != nil {
		t.Fatalf("exact replay error = %v", err)
	}
	if !reflect.DeepEqual(replayed, evidence) || repository.calls != 0 {
		t.Fatalf("replay = %#v, calls = %d, want exact evidence and no mutation", replayed, repository.calls)
	}

	command.Reason = "different reason"
	_, err = workflow.UpdateTournamentConfiguration(t.Context(), command)
	if !errors.Is(err, domain.ErrConflict) || repository.calls != 0 {
		t.Fatalf("changed replay error = %v, calls = %d, want conflict and no mutation", err, repository.calls)
	}
}

func TestTournamentConfigurationWorkflowUpdatesSeriesAndSwissRound(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	firstParticipant, secondParticipant, thirdParticipant, fourthParticipant := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seriesOne, seriesTwo := uuid.New(), uuid.New()
	authority.Series = []ConfigurationSeries{
		{ID: seriesOne, Stage: domain.TournamentStageSwiss, RoundNumber: 1, Revision: 3, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
		{ID: seriesTwo, Stage: domain.TournamentStageSwiss, RoundNumber: 1, Revision: 2, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
	}
	authority.Rounds = []ConfigurationRound{{
		ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 5,
		ParticipantIDs: []uuid.UUID{firstParticipant, secondParticipant, thirdParticipant, fourthParticipant},
		SeriesIDs:      []uuid.UUID{seriesOne, seriesTwo},
	}}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	seriesEvidence, err := workflow.UpdateUnstartedSeries(t.Context(), inbound.AdminUpdateUnstartedSeriesCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), SeriesID: seriesOne,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedSeriesRevision: 3, Confirmed: true, Reason: "select Swiss category",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryCrypto},
	})
	if err != nil {
		t.Fatalf("UpdateUnstartedSeries() error = %v", err)
	}
	if len(seriesEvidence.AffectedArtifactIDs) != 1 || repository.calls != 1 {
		t.Fatalf("series evidence/calls = %#v/%d, want one artifact and one mutation", seriesEvidence.AffectedArtifactIDs, repository.calls)
	}

	repository.authority = authority
	roundEvidence, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), RoundNumber: 1,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 5, Confirmed: true, Reason: "revise unstarted Swiss round",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryCrypto},
		ManualPairings: []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: firstParticipant, SecondParticipantID: secondParticipant}, {FirstParticipantID: thirdParticipant, SecondParticipantID: fourthParticipant}},
	})
	if err != nil {
		t.Fatalf("ReviseSwissRound() error = %v", err)
	}
	if len(roundEvidence.AffectedArtifactIDs) != 3 || repository.calls != 2 {
		t.Fatalf("round evidence/calls = %#v/%d, want round plus two series and two mutations", roundEvidence.AffectedArtifactIDs, repository.calls)
	}
}

func TestTournamentConfigurationWorkflowRejectsSwissPairingFromEarlierRound(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	seriesIDs := []uuid.UUID{uuid.New(), uuid.New()}
	authority.Series = []ConfigurationSeries{
		{ID: seriesIDs[0], Stage: domain.TournamentStageSwiss, RoundNumber: 2, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
		{ID: seriesIDs[1], Stage: domain.TournamentStageSwiss, RoundNumber: 2, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
	}
	authority.Rounds = []ConfigurationRound{
		{ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 1, ParticipantIDs: participants, Pairings: []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[0], SecondParticipantID: participants[1]}, {FirstParticipantID: participants[2], SecondParticipantID: participants[3]}}},
		{ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 2, Revision: 4, ParticipantIDs: participants, SeriesIDs: seriesIDs},
	}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	_, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), RoundNumber: 2,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 4, Confirmed: true, Reason: "replace a repeated pre-start pairing",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings: []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[0], SecondParticipantID: participants[1]}, {FirstParticipantID: participants[2], SecondParticipantID: participants[3]}},
	})
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("ReviseSwissRound() error = %v, want validation error", err)
	}
	if repository.calls != 0 {
		t.Fatalf("ExecuteMutation calls = %d, want 0", repository.calls)
	}
}

func TestTournamentConfigurationWorkflowCarriesAuthoritativeSwissMeetingCounts(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	seriesIDs := []uuid.UUID{uuid.New(), uuid.New()}
	authority.Series = []ConfigurationSeries{
		{ID: seriesIDs[0], Stage: domain.TournamentStageSwiss, RoundNumber: 2, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
		{ID: seriesIDs[1], Stage: domain.TournamentStageSwiss, RoundNumber: 2, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
	}
	previousPair := inbound.AdminConfigurationParticipantPair{FirstParticipantID: participants[0], SecondParticipantID: participants[1]}
	authority.Rounds = []ConfigurationRound{
		{ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 1, ParticipantIDs: participants, Pairings: []inbound.AdminConfigurationParticipantPair{previousPair, {FirstParticipantID: participants[2], SecondParticipantID: participants[3]}}},
		{ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 2, Revision: 4, ParticipantIDs: participants, SeriesIDs: seriesIDs},
	}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	_, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), RoundNumber: 2,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 4, Confirmed: true, Reason: "replace unstarted pairings",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings: []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[0], SecondParticipantID: participants[2]}, {FirstParticipantID: participants[1], SecondParticipantID: participants[3]}},
	})
	if err != nil {
		t.Fatalf("ReviseSwissRound() error = %v", err)
	}
	if repository.calls != 1 || repository.last.RoundChange == nil {
		t.Fatalf("mutation calls/change = %d/%#v, want one round mutation", repository.calls, repository.last.RoundChange)
	}
	counts := repository.last.RoundChange.PriorMeetingCounts
	if counts[swissusecase.NewPairKey(previousPair.FirstParticipantID, previousPair.SecondParticipantID)] != 1 {
		t.Fatalf("prior count for historical pairing = %d, want 1", counts[swissusecase.NewPairKey(previousPair.FirstParticipantID, previousPair.SecondParticipantID)])
	}
	for _, pairing := range repository.last.RoundChange.Next.Pairings {
		if counts[swissusecase.NewPairKey(pairing.FirstParticipantID, pairing.SecondParticipantID)] != 0 {
			t.Fatalf("proposed pairing %v has a non-zero prior count", pairing)
		}
	}
}

func TestTournamentConfigurationWorkflowPropagatesRepositoryRollbackError(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	authority.Artifacts = []ConfigurationArtifact{{Kind: "swiss-round", ID: uuid.New(), Stage: domain.TournamentStageSwiss, Revision: 1, State: ConfigurationArtifactPlanned}}
	sentinel := errors.New("transaction rolled back")
	repository := &configurationRepositoryStub{authority: authority, err: sentinel}
	workflow := NewTournamentConfigurationWorkflow(repository)
	_, err := workflow.UpdateTournamentConfiguration(t.Context(), configurationUpdateCommand(authority, uuid.New()))
	if !errors.Is(err, sentinel) || repository.calls != 1 {
		t.Fatalf("error = %v, calls = %d, want repository rollback error and one atomic call", err, repository.calls)
	}
}

var authorityOperatorID = uuid.MustParse("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee")

func configurationUpdateCommand(authority ConfigurationAuthority, commandID uuid.UUID) inbound.AdminUpdateTournamentConfigurationCommand {
	return inbound.AdminUpdateTournamentConfigurationCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: commandID,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedConfigurationRevision: authority.Configuration.Revision,
		Confirmed: true, Reason: "update Swiss configuration",
		SwissDefault:     inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryCrypto}},
		SemifinalDefault: authority.SemifinalDefault,
	}
}

func configurationAuthorityFixture(t *testing.T) ConfigurationAuthority {
	t.Helper()
	tournamentID := uuid.New()
	bo1ID, bo3ID := uuid.New(), uuid.New()
	normalTask, goldenTask := uuid.New(), uuid.New()
	configuration, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID: tournamentID,
		CategoryPools: []domain.CategoryPoolRevision{
			{ID: bo1ID, Revision: 1, Format: domain.SeriesFormatBO1, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics}},
			{ID: bo3ID, Revision: 1, Format: domain.SeriesFormatBO3, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryReverse, domain.CategoryPwn}},
		},
		NormalPool: domain.TaskPoolRevision{ID: normalTask, Revision: 1, Kind: domain.AssignmentTaskKindNormal, Versions: []domain.TaskVersionRef{{TaskID: uuid.New(), Version: 1}}},
		GoldenPool: domain.TaskPoolRevision{ID: goldenTask, Revision: 1, Kind: domain.AssignmentTaskKindGolden, Versions: []domain.TaskVersionRef{{TaskID: uuid.New(), Version: 1}}},
		StageDefaults: []domain.StageContentDefault{
			{Stage: domain.TournamentStageSwiss, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1ID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageGolden, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1ID, TaskPoolKind: domain.AssignmentTaskKindGolden},
			{Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1ID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3, CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3ID, TaskPoolKind: domain.AssignmentTaskKindNormal},
		},
	})
	if err != nil {
		t.Fatalf("CreateContentConfiguration() error = %v", err)
	}
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return ConfigurationAuthority{
		TournamentID: tournamentID, ProjectionRevisionID: uuid.New(), ProjectionRevision: 7,
		Configuration: configuration, TournamentState: domain.TournamentStateRegistration, TournamentRevision: 3, UpdatedAt: now,
		SwissDefault:     inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}},
		GoldenDefault:    inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryCrypto}},
		SemifinalDefault: inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeDraft, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics}},
		FinalDefault:     inbound.AdminConfigurationStageDefault{Mode: domain.CategoryModeDraft, Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryReverse, domain.CategoryPwn}},
	}
}
