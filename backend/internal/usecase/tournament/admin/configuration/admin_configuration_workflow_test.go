package configuration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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

func TestTournamentConfigurationWorkflowPersistsConfiguredReserveCount(t *testing.T) {
	t.Parallel()

	for _, reserveCount := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("reserve_count_%d", reserveCount), func(t *testing.T) {
			t.Parallel()
			authority := configurationAuthorityFixture(t)
			authority.Configuration.ReserveCount = 2
			repository := &configurationRepositoryStub{authority: authority}
			workflow := NewTournamentConfigurationWorkflow(repository)
			command := configurationUpdateCommand(authority, uuid.New())
			command.ReserveCount = reserveCount
			command.SwissDefault = authority.SwissDefault
			command.SemifinalDefault = authority.SemifinalDefault

			evidence, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
			if err != nil {
				t.Fatalf("UpdateTournamentConfiguration() error = %v", err)
			}
			if reserveCount == authority.Configuration.ReserveCount {
				if repository.calls != 0 || evidence.NextConfigurationRevision != authority.Configuration.Revision {
					t.Fatalf("unchanged reserve count mutated: calls=%d evidence=%#v", repository.calls, evidence)
				}
				return
			}
			if repository.calls != 1 {
				t.Fatalf("ExecuteMutation calls = %d, want 1", repository.calls)
			}
			if repository.last.NextConfiguration.ReserveCount != reserveCount {
				t.Fatalf("next reserve count = %d, want %d", repository.last.NextConfiguration.ReserveCount, reserveCount)
			}
		})
	}
}

func TestTournamentConfigurationWorkflowReserveCountHonorsCutoffAndStaleRevision(t *testing.T) {
	t.Parallel()

	authority := configurationAuthorityFixture(t)
	authority.Configuration.ReserveCount = 2
	authority.Artifacts = []ConfigurationArtifact{{
		Kind: "swiss-round", ID: uuid.New(), Stage: domain.TournamentStageSwiss,
		Revision: 1, State: ConfigurationArtifactActive, Started: true,
	}}
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	command := configurationUpdateCommand(authority, uuid.New())
	command.ReserveCount = 1
	command.SwissDefault = authority.SwissDefault
	command.SemifinalDefault = authority.SemifinalDefault

	_, err := workflow.UpdateTournamentConfiguration(t.Context(), command)
	if !errors.Is(err, ErrCutoff) || !errors.Is(err, domain.ErrConflict) || repository.calls != 0 {
		t.Fatalf("cutoff error = %v, calls = %d", err, repository.calls)
	}

	authority.Artifacts = nil
	repository.authority = authority
	command.ExpectedProjectionRevision--
	_, err = workflow.UpdateTournamentConfiguration(t.Context(), command)
	var conflict *ConfigurationRevisionConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, domain.ErrConflict) || repository.calls != 0 {
		t.Fatalf("stale error = %v, calls = %d", err, repository.calls)
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
	var transportConflict *inbound.AdminRevisionConflictError
	if !errors.As(err, &transportConflict) || transportConflict.ExpectedRevision != command.ExpectedProjectionRevision ||
		transportConflict.CurrentRevision != authority.ProjectionRevision || transportConflict.CurrentState != authority.TournamentState {
		t.Fatalf("transport conflict = %#v, want projection %d/%d state %s", transportConflict,
			command.ExpectedProjectionRevision, authority.ProjectionRevision, authority.TournamentState)
	}
	if repository.calls != 0 {
		t.Fatalf("ExecuteMutation calls = %d, want 0", repository.calls)
	}
}

func TestConfigurationAuthorityRejectsHalfNullByeRevisionLink(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	revisionID := uuid.New()
	for name, round := range map[string]ConfigurationRound{
		"participant without revision": {
			ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 1,
			ParticipantIDs: []uuid.UUID{participantID}, ByeParticipantID: &participantID,
		},
		"revision without participant": {
			ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 1,
			ParticipantIDs: []uuid.UUID{participantID}, ByeRevisionID: &revisionID,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			authority := configurationAuthorityFixture(t)
			authority.Rounds = []ConfigurationRound{round}
			if err := authority.Validate(authority.TournamentID); !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("Validate() error = %v, want ErrInternal", err)
			}
		})
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
	if repository.last.RoundChange == nil || repository.last.RoundChange.Bye != nil || repository.last.RoundChange.Next.ByeRevisionID != nil {
		t.Fatalf("even Swiss round bye change = %#v, want nil selection and revision", repository.last.RoundChange)
	}
}

func TestTournamentConfigurationWorkflowSelectsEligibleChangedBye(t *testing.T) {
	t.Parallel()

	authority, participants, roundID := configurationOddSwissAuthority(t, 1)
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	commandID := uuid.New()
	requested := participants[0]
	_, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: commandID, RoundNumber: 1,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 5, Confirmed: true, Reason: "select eligible Swiss bye",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings:         []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[1], SecondParticipantID: participants[2]}, {FirstParticipantID: participants[3], SecondParticipantID: participants[4]}},
		ManualByeParticipantID: &requested,
	})
	if err != nil {
		t.Fatalf("ReviseSwissRound() error = %v", err)
	}
	if repository.calls != 1 || repository.last.RoundChange == nil || repository.last.RoundChange.Bye == nil {
		t.Fatalf("mutation calls/bye = %d/%#v, want one mutation with bye selection", repository.calls, repository.last.RoundChange)
	}
	selection := repository.last.RoundChange.Bye
	if selection.ParticipantID != requested || selection.Evidence.ID != configurationByeEvidenceID(commandID, roundID) || selection.Evidence.OwnerID != roundID {
		t.Fatalf("bye selection = %#v, want requested participant and command-round evidence", selection)
	}
	if repository.last.RoundChange.Next.ByeParticipantID == nil || *repository.last.RoundChange.Next.ByeParticipantID != requested ||
		repository.last.RoundChange.Next.ByeRevisionID == nil || *repository.last.RoundChange.Next.ByeRevisionID != selection.Evidence.ID {
		t.Fatalf("next bye links = %v/%v, want participant and evidence links", repository.last.RoundChange.Next.ByeParticipantID, repository.last.RoundChange.Next.ByeRevisionID)
	}
	if err := selection.Evidence.Validate(); err != nil {
		t.Fatalf("bye evidence Validate() error = %v", err)
	}
}

func TestTournamentConfigurationWorkflowRejectsWrongByeBeforeMutation(t *testing.T) {
	t.Parallel()

	authority, participants, _ := configurationOddSwissAuthority(t, 1)
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	requested := participants[1]
	_, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), RoundNumber: 1,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 5, Confirmed: true, Reason: "request ineligible Swiss bye",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings:         []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[0], SecondParticipantID: participants[2]}, {FirstParticipantID: participants[3], SecondParticipantID: participants[4]}},
		ManualByeParticipantID: &requested,
	})
	var mismatch *ManualByeMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, ErrManualByeMismatch) || repository.calls != 0 {
		t.Fatalf("ReviseSwissRound() error = %v, mismatch = %#v, calls = %d, want atomic manual bye rejection", err, mismatch, repository.calls)
	}
}

func TestTournamentConfigurationWorkflowRejectsRepeatedByeBeforeMutation(t *testing.T) {
	t.Parallel()

	authority, participants, _ := configurationOddSwissAuthority(t, 2)
	previousBye := participants[0]
	previousByeRevision := uuid.New()
	authority.Rounds = append([]ConfigurationRound{{
		ID: uuid.New(), Stage: domain.TournamentStageSwiss, Number: 1, Revision: 1,
		ParticipantIDs: participants, ByeParticipantID: &previousBye, ByeRevisionID: &previousByeRevision,
	}}, authority.Rounds...)
	repository := &configurationRepositoryStub{authority: authority}
	workflow := NewTournamentConfigurationWorkflow(repository)
	_, err := workflow.ReviseSwissRound(t.Context(), inbound.AdminReviseSwissRoundCommand{
		Operator: inbound.AdminOperatorIdentity{ActorID: authorityOperatorID}, TournamentID: authority.TournamentID, CommandID: uuid.New(), RoundNumber: 2,
		ExpectedProjectionRevision: authority.ProjectionRevision, ExpectedRoundRevision: 5, Confirmed: true, Reason: "request repeated Swiss bye",
		CategoryMode: domain.CategoryModeAdmin, Categories: []domain.Category{domain.CategoryWeb},
		ManualPairings:         []inbound.AdminConfigurationParticipantPair{{FirstParticipantID: participants[1], SecondParticipantID: participants[2]}, {FirstParticipantID: participants[3], SecondParticipantID: participants[4]}},
		ManualByeParticipantID: &previousBye,
	})
	var mismatch *ManualByeMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, ErrManualByeMismatch) || repository.calls != 0 {
		t.Fatalf("ReviseSwissRound() error = %v, mismatch = %#v, calls = %d, want repeated bye rejection", err, mismatch, repository.calls)
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

func configurationOddSwissAuthority(t *testing.T, roundNumber int) (ConfigurationAuthority, []uuid.UUID, uuid.UUID) {
	t.Helper()
	authority := configurationAuthorityFixture(t)
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	seriesIDs := []uuid.UUID{uuid.New(), uuid.New()}
	authority.Series = []ConfigurationSeries{
		{ID: seriesIDs[0], Stage: domain.TournamentStageSwiss, RoundNumber: roundNumber, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
		{ID: seriesIDs[1], Stage: domain.TournamentStageSwiss, RoundNumber: roundNumber, Revision: 1, Mode: domain.CategoryModeRandom, Categories: []domain.Category{domain.CategoryWeb}, State: domain.SeriesStatePlanned},
	}
	roundID := uuid.New()
	authority.Rounds = []ConfigurationRound{{
		ID: roundID, Stage: domain.TournamentStageSwiss, Number: roundNumber, Revision: 5,
		ParticipantIDs: participants, SeriesIDs: seriesIDs,
	}}
	authority.Standings = make([]SwissStandingView, len(participants))
	for index, participantID := range participants {
		authority.Standings[index] = SwissStandingView{
			ParticipantID: participantID, Position: index + 1, Points: index + 1, Buchholz: index + 1,
			EffectiveTimeMS: int64(index + 1),
		}
	}
	return authority, participants, roundID
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

func TestSwissRoundRandomSelectionAcceptsPoolWithoutRelaxingStageDefaults(t *testing.T) {
	t.Parallel()
	authority := configurationAuthorityFixture(t)
	pool := []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics}
	if err := authority.validateSwissRoundSelection(domain.CategoryModeRandom, pool); err != nil {
		t.Fatalf("full random round pool rejected: %v", err)
	}
	if err := authority.validateStageSelection(domain.TournamentStageSwiss, ConfigurationStageDefault{
		Mode: domain.CategoryModeRandom, Categories: pool,
	}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("stage default must still require its resolved category, got %v", err)
	}
	for _, invalid := range [][]domain.Category{
		{domain.CategoryWeb, domain.CategoryCrypto},
		{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn},
		{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryWeb},
	} {
		if err := authority.validateSwissRoundSelection(domain.CategoryModeRandom, invalid); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("invalid random round pool %v accepted: %v", invalid, err)
		}
	}
}
