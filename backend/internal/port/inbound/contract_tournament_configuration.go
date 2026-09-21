package usecase

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TournamentConfigurationUseCase is the transport-neutral boundary for the
// operator changes that are allowed before a derived series or round starts.
// It deliberately exposes category identities and revisions, but never task
// identities or task pool contents.
type TournamentConfigurationUseCase interface {
	GetTournamentConfiguration(
		ctx context.Context,
		query AdminTournamentConfigurationQuery,
	) (AdminTournamentConfigurationView, error)
	UpdateTournamentConfiguration(
		ctx context.Context,
		command AdminUpdateTournamentConfigurationCommand,
	) (AdminTournamentConfigurationMutationEvidence, error)
	UpdateUnstartedSeries(
		ctx context.Context,
		command AdminUpdateUnstartedSeriesCommand,
	) (AdminTournamentConfigurationMutationEvidence, error)
	ReviseSwissRound(
		ctx context.Context,
		command AdminReviseSwissRoundCommand,
	) (AdminTournamentConfigurationMutationEvidence, error)
}

type AdminTournamentConfigurationQuery struct {
	Operator     AdminOperatorIdentity
	TournamentID uuid.UUID
}

// AdminConfigurationStageDefault is the effective category choice for one
// stage. The final stage is returned by the server and cannot be changed.
type AdminConfigurationStageDefault struct {
	Mode                   domain.CategoryMode
	Categories             []domain.Category
	CategoryPoolRevisionID uuid.UUID
	CategoryPoolRevision   int64
}

type AdminConfigurationCategoryPoolView struct {
	ID         uuid.UUID
	Revision   int64
	Format     domain.SeriesFormat
	Categories []domain.Category
}

type AdminConfigurationSeriesView struct {
	ID                     uuid.UUID
	Stage                  domain.TournamentStage
	RoundNumber            int
	Revision               int64
	Mode                   domain.CategoryMode
	Categories             []domain.Category
	CategoryPoolRevisionID uuid.UUID
	CategoryPoolRevision   int64
	Locked                 bool
	Started                bool
	Consumed               bool
	Disclosed              bool
	UnlockIntents          []AdminConfigurationUnlockIntent
}

type AdminConfigurationRoundView struct {
	ID               uuid.UUID
	RoundNumber      int
	Revision         int64
	Mode             domain.CategoryMode
	Categories       []domain.Category
	Pairings         []AdminConfigurationParticipantPair
	ByeParticipantID *uuid.UUID
	Locked           bool
	Started          bool
	Consumed         bool
	Disclosed        bool
	UnlockIntents    []AdminConfigurationUnlockIntent
}

type AdminTournamentConfigurationView struct {
	TournamentID          uuid.UUID
	ProjectionRevisionID  uuid.UUID
	ProjectionRevision    int64
	ConfigurationRevision int64
	ReserveCount          int
	CategoryPools         []AdminConfigurationCategoryPoolView
	SwissDefault          AdminConfigurationStageDefault
	GoldenDefault         AdminConfigurationStageDefault
	SemifinalDefault      AdminConfigurationStageDefault
	FinalDefault          AdminConfigurationStageDefault
	Series                []AdminConfigurationSeriesView
	Rounds                []AdminConfigurationRoundView
	UpdatedAt             time.Time
}

type AdminUpdateTournamentConfigurationCommand struct {
	Operator                      AdminOperatorIdentity
	TournamentID                  uuid.UUID
	CommandID                     uuid.UUID
	ExpectedProjectionRevision    int64
	ExpectedConfigurationRevision int64
	ReserveCount                  int
	Confirmed                     bool
	Reason                        string
	SwissDefault                  AdminConfigurationStageDefault
	SemifinalDefault              AdminConfigurationStageDefault
	UnlockIntents                 []AdminConfigurationUnlockIntent
}

type AdminConfigurationParticipantPair struct {
	FirstParticipantID  uuid.UUID `json:"first_participant_id"`
	SecondParticipantID uuid.UUID `json:"second_participant_id"`
}

type AdminConfigurationUnlockIntent struct {
	ReservationID     uuid.UUID         `json:"reservation_id"`
	OwnerID           uuid.UUID         `json:"owner_id"`
	SourceRevisionID  uuid.UUID         `json:"source_revision_id"`
	ExpectedRevision  int64             `json:"expected_revision"`
	ExpectedUsed      bool              `json:"expected_used"`
	ExpectedDisclosed bool              `json:"expected_disclosed"`
	EvidenceDigest    [sha256.Size]byte `json:"evidence_digest"`
	BindingDigest     [sha256.Size]byte `json:"binding_digest"`
}

type AdminUpdateUnstartedSeriesCommand struct {
	Operator                   AdminOperatorIdentity
	TournamentID               uuid.UUID
	CommandID                  uuid.UUID
	SeriesID                   uuid.UUID
	ExpectedProjectionRevision int64
	ExpectedSeriesRevision     int64
	Confirmed                  bool
	Reason                     string
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	UnlockIntents              []AdminConfigurationUnlockIntent
}

type AdminReviseSwissRoundCommand struct {
	Operator                   AdminOperatorIdentity
	TournamentID               uuid.UUID
	CommandID                  uuid.UUID
	RoundNumber                int
	ExpectedProjectionRevision int64
	ExpectedRoundRevision      int64
	Confirmed                  bool
	Reason                     string
	CategoryMode               domain.CategoryMode
	Categories                 []domain.Category
	ManualPairings             []AdminConfigurationParticipantPair
	ManualByeParticipantID     *uuid.UUID
	UnlockIntents              []AdminConfigurationUnlockIntent
}

type AdminConfigurationArtifactView struct {
	Kind                string                 `json:"kind"`
	ID                  uuid.UUID              `json:"id"`
	Stage               domain.TournamentStage `json:"stage"`
	PreviousRevisionID  uuid.UUID              `json:"previous_revision_id"`
	SuccessorRevisionID uuid.UUID              `json:"successor_revision_id"`
}

type AdminConfigurationMutationEvidence struct {
	CommandID                     uuid.UUID                        `json:"command_id"`
	TournamentID                  uuid.UUID                        `json:"tournament_id"`
	OperatorID                    uuid.UUID                        `json:"operator_id"`
	Reason                        string                           `json:"reason"`
	RequestedAt                   time.Time                        `json:"requested_at"`
	ValidationDigest              [sha256.Size]byte                `json:"validation_digest"`
	PreviousConfigurationRevision int64                            `json:"previous_configuration_revision"`
	NextConfigurationRevision     int64                            `json:"next_configuration_revision"`
	AffectedArtifactIDs           []uuid.UUID                      `json:"affected_artifact_ids"`
	SupersededArtifactIDs         []uuid.UUID                      `json:"superseded_artifact_ids"`
	RebuiltArtifactIDs            []uuid.UUID                      `json:"rebuilt_artifact_ids"`
	AffectedArtifacts             []AdminConfigurationArtifactView `json:"affected_artifacts"`
	UnlockIntents                 []AdminConfigurationUnlockIntent `json:"unlock_intents"`
}

// AdminTournamentConfigurationMutationEvidence is the explicit long-form
// name used by the configuration boundary.
type AdminTournamentConfigurationMutationEvidence = AdminConfigurationMutationEvidence
