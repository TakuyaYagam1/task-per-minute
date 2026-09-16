package progression

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type progressionGoldenGroup struct {
	revisionID           uuid.UUID
	revisionNo           int
	id                   uuid.UUID
	settlementRevisionID uuid.UUID
	finalizedAt          time.Time
	from                 int
	to                   int
	attempts             map[uuid.UUID]progressionGoldenAttempt
	commits              map[uuid.UUID]progressionGoldenCommit
}

type progressionGoldenAttempt struct {
	id uuid.UUID
}

type progressionGoldenCommit struct {
	id            uuid.UUID
	attemptID     uuid.UUID
	participantID uuid.UUID
	position      int
}

type progressionGoldenLedgerRevision struct {
	id          uuid.UUID
	groupID     uuid.UUID
	revision    int64
	previousID  *uuid.UUID
	digest      [sha256.Size]byte
	finalizedAt time.Time
	attempts    map[uuid.UUID]progressionGoldenLedgerAttempt
	commitments map[uuid.UUID]progressionGoldenLedgerCommitment
}

type progressionGoldenLedgerAttempt struct {
	id                   uuid.UUID
	submissionRevisionID uuid.UUID
	submissionRevision   int64
	attemptNo            int
	orderCount           int
	waveID               uuid.UUID
	assignmentID         uuid.UUID
	snapshotID           uuid.UUID
	taskID               uuid.UUID
}

type progressionGoldenLedgerCommitment struct {
	commitID       uuid.UUID
	attemptID      uuid.UUID
	participantID  uuid.UUID
	position       int
	submissionID   uint64
	evidenceDigest [sha256.Size]byte
}

func progressionGoldenSettlements(
	authority tournamentprogression.Authority,
	settlementRows []sqlc.LockTournamentProgressionGoldenSettlementsRow,
	attemptRows []sqlc.LockTournamentProgressionGoldenAttemptsRow,
	commitRows []sqlc.LockTournamentProgressionGoldenPositionCommitsRow,
	ledgerRows []sqlc.LockTournamentProgressionGoldenPositionLedgerRow,
	seals []sqlc.GoldenPositionLedgerRevisionSeal,
) ([]playoff.Top4GoldenSettlement, error) {
	if !validTournamentProgressionAuthority(authority) ||
		authority.Tournament.State != domain.TournamentStateGolden {
		return nil, domain.ErrValidation
	}
	groups, err := progressionGoldenGroups(authority, settlementRows, attemptRows, commitRows)
	if err != nil {
		return nil, fmt.Errorf("restore Golden groups: %w", err)
	}
	ledgers, err := progressionGoldenLedgers(groups, ledgerRows, seals, authority)
	if err != nil {
		return nil, fmt.Errorf("restore Golden ledgers: %w", err)
	}
	settlements := make([]playoff.Top4GoldenSettlement, 0, len(groups))
	groupIDs := make([]uuid.UUID, 0, len(groups))
	for groupID := range groups {
		groupIDs = append(groupIDs, groupID)
	}
	slices.SortFunc(groupIDs, func(left, right uuid.UUID) int {
		if order := cmp.Compare(groups[left].from, groups[right].from); order != 0 {
			return order
		}
		if order := cmp.Compare(groups[left].to, groups[right].to); order != 0 {
			return order
		}
		return cmp.Compare(left.String(), right.String())
	})
	for _, groupID := range groupIDs {
		group := groups[groupID]
		ledger, found := ledgers[group.revisionID]
		if !found {
			return nil, domain.ErrConflict
		}
		settlement, err := progressionGoldenSettlement(authority, group, ledger)
		if err != nil {
			return nil, fmt.Errorf("restore Golden settlement: %w", err)
		}
		settlements = append(settlements, settlement)
	}
	return settlements, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionGoldenGroups(
	authority tournamentprogression.Authority,
	settlementRows []sqlc.LockTournamentProgressionGoldenSettlementsRow,
	attemptRows []sqlc.LockTournamentProgressionGoldenAttemptsRow,
	commitRows []sqlc.LockTournamentProgressionGoldenPositionCommitsRow,
) (map[uuid.UUID]progressionGoldenGroup, error) {
	if len(settlementRows) == 0 {
		return nil, domain.ErrConflict
	}
	groups := make(map[uuid.UUID]progressionGoldenGroup)
	for _, row := range settlementRows {
		if row.GroupRevisionID == uuid.Nil || row.GroupID == uuid.Nil ||
			row.SourceProjectionRevisionID != authority.ProjectionRevisionID ||
			row.SourceProjectionRevision != authority.ProjectionRevision ||
			row.GroupRevisionNumber < 1 ||
			row.PositionFrom < 1 || row.PositionTo < row.PositionFrom ||
			int(row.PositionTo) > domain.TournamentMaxParticipants ||
			!row.AttemptID.Valid || row.AttemptID.UUID == uuid.Nil || row.AttemptState == nil ||
			domain.GoldenAttemptState(*row.AttemptState) != domain.GoldenAttemptStateCompleted ||
			!row.PositionCommitID.Valid || row.PositionCommitID.UUID == uuid.Nil ||
			!row.ParticipantID.Valid || row.ParticipantID.UUID == uuid.Nil || row.Position == nil ||
			int(*row.Position) < int(row.PositionFrom) || int(*row.Position) > int(row.PositionTo) {
			return nil, fmt.Errorf("invalid persisted Golden group row: %w", domain.ErrConflict)
		}
		group, found := groups[row.GroupRevisionID]
		if !found {
			group = progressionGoldenGroup{
				revisionID: row.GroupRevisionID, id: row.GroupID,
				revisionNo:           int(row.GroupRevisionNumber),
				settlementRevisionID: row.SettlementRevisionID.UUID,
				finalizedAt:          row.RuntimeFinalizedAt.Time,
				from:                 int(row.PositionFrom), to: int(row.PositionTo),
				attempts: make(map[uuid.UUID]progressionGoldenAttempt),
				commits:  make(map[uuid.UUID]progressionGoldenCommit),
			}
		} else if group.id != row.GroupID || group.revisionNo != int(row.GroupRevisionNumber) ||
			group.from != int(row.PositionFrom) || group.to != int(row.PositionTo) ||
			group.settlementRevisionID != row.SettlementRevisionID.UUID ||
			!group.finalizedAt.Equal(row.RuntimeFinalizedAt.Time) {
			return nil, domain.ErrConflict
		}
		if previous, exists := group.attempts[row.AttemptID.UUID]; exists && previous.id != row.AttemptID.UUID {
			return nil, domain.ErrConflict
		}
		group.attempts[row.AttemptID.UUID] = progressionGoldenAttempt{id: row.AttemptID.UUID}
		commit := progressionGoldenCommit{
			id: row.PositionCommitID.UUID, attemptID: row.AttemptID.UUID,
			participantID: row.ParticipantID.UUID, position: int(*row.Position),
		}
		if previous, exists := group.commits[commit.id]; exists && previous != commit {
			return nil, domain.ErrConflict
		}
		group.commits[commit.id] = commit
		groups[row.GroupRevisionID] = group
	}
	if err := progressionGoldenAttemptsMatch(groups, attemptRows); err != nil {
		return nil, fmt.Errorf("golden attempts differ from groups: %w", err)
	}
	if err := progressionGoldenCommitsMatch(groups, commitRows); err != nil {
		return nil, fmt.Errorf("golden commits differ from groups: %w", err)
	}
	return groups, nil
}

func progressionGoldenAttemptsMatch(
	groups map[uuid.UUID]progressionGoldenGroup,
	rows []sqlc.LockTournamentProgressionGoldenAttemptsRow,
) error {
	seen := make(map[[2]uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		group, found := groups[row.GroupRevisionID]
		if !found || row.AttemptID == uuid.Nil ||
			domain.GoldenAttemptState(row.AttemptState) != domain.GoldenAttemptStateCompleted {
			return domain.ErrConflict
		}
		if _, found = group.attempts[row.AttemptID]; !found {
			return domain.ErrConflict
		}
		key := [2]uuid.UUID{row.GroupRevisionID, row.AttemptID}
		if _, duplicate := seen[key]; duplicate {
			return domain.ErrConflict
		}
		seen[key] = struct{}{}
	}
	for groupID, group := range groups {
		for attemptID := range group.attempts {
			if _, found := seen[[2]uuid.UUID{groupID, attemptID}]; !found {
				return domain.ErrConflict
			}
		}
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func progressionGoldenCommitsMatch(
	groups map[uuid.UUID]progressionGoldenGroup,
	rows []sqlc.LockTournamentProgressionGoldenPositionCommitsRow,
) error {
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		group, found := groups[row.GroupRevisionID]
		if !found || row.PositionCommitID == uuid.Nil || row.AttemptID == uuid.Nil ||
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			row.ParticipantID == uuid.Nil || row.Position < int16(group.from) || row.Position > int16(group.to) {
			return domain.ErrConflict
		}
		commit, found := group.commits[row.PositionCommitID]
		if !found || commit.attemptID != row.AttemptID || commit.participantID != row.ParticipantID ||
			commit.position != int(row.Position) {
			return domain.ErrConflict
		}
		if _, duplicate := seen[row.PositionCommitID]; duplicate {
			return domain.ErrConflict
		}
		seen[row.PositionCommitID] = struct{}{}
	}
	for _, group := range groups {
		for commitID := range group.commits {
			if _, found := seen[commitID]; !found {
				return domain.ErrConflict
			}
		}
	}
	return nil
}

func progressionGoldenLedgers(
	groups map[uuid.UUID]progressionGoldenGroup,
	rows []sqlc.LockTournamentProgressionGoldenPositionLedgerRow,
	seals []sqlc.GoldenPositionLedgerRevisionSeal,
	authority tournamentprogression.Authority,
) (map[uuid.UUID][]progressionGoldenLedgerRevision, error) {
	ledgers := make(map[uuid.UUID]map[uuid.UUID]progressionGoldenLedgerRevision, len(groups))
	for _, row := range rows {
		if _, found := groups[row.GroupRevisionID]; !found {
			continue
		}
		revision, err := progressionGoldenLedgerRow(row)
		if err != nil {
			return nil, err
		}
		if ledgers[revision.groupID] == nil {
			ledgers[revision.groupID] = make(map[uuid.UUID]progressionGoldenLedgerRevision)
		}
		if existing, found := ledgers[revision.groupID][revision.id]; found {
			merged, mergeErr := progressionGoldenMergeLedgerRevision(existing, revision)
			if mergeErr != nil {
				return nil, mergeErr
			}
			ledgers[revision.groupID][revision.id] = merged
			continue
		}
		ledgers[revision.groupID][revision.id] = revision
	}
	ordered := make(map[uuid.UUID][]progressionGoldenLedgerRevision, len(groups))
	for groupID := range groups {
		byID, found := ledgers[groupID]
		if !found || len(byID) == 0 {
			return nil, domain.ErrConflict
		}
		chain := make([]progressionGoldenLedgerRevision, 0, len(byID))
		for _, revision := range byID {
			chain = append(chain, revision)
		}
		slices.SortFunc(chain, func(left, right progressionGoldenLedgerRevision) int {
			return cmp.Compare(left.revision, right.revision)
		})
		if err := progressionGoldenLedgerChain(chain, seals, authority); err != nil {
			return nil, err
		}
		ordered[groupID] = chain
	}
	return ordered, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionGoldenLedgerRow(
	row sqlc.LockTournamentProgressionGoldenPositionLedgerRow,
) (progressionGoldenLedgerRevision, error) {
	digest, ok := progressionDigest(row.PayloadDigest)
	if !ok || row.LedgerRevisionID == uuid.Nil || row.GroupRevisionID == uuid.Nil || row.RevisionNumber < 1 ||
		!row.FinalizedAt.Valid || !domain.IsValidServerTime(row.FinalizedAt.Time.UTC()) {
		return progressionGoldenLedgerRevision{}, domain.ErrConflict
	}
	revision := progressionGoldenLedgerRevision{
		id: row.LedgerRevisionID, groupID: row.GroupRevisionID, revision: row.RevisionNumber,
		digest: digest, finalizedAt: row.FinalizedAt.Time.UTC(),
		attempts:    make(map[uuid.UUID]progressionGoldenLedgerAttempt),
		commitments: make(map[uuid.UUID]progressionGoldenLedgerCommitment),
	}
	if row.PreviousRevisionID.Valid {
		if row.PreviousRevisionID.UUID == uuid.Nil {
			return progressionGoldenLedgerRevision{}, domain.ErrConflict
		}
		previous := row.PreviousRevisionID.UUID
		revision.previousID = &previous
	}
	if !row.AttemptID.Valid {
		if row.SubmissionRevisionID.Valid || row.SubmissionRevision != nil || row.AttemptNumber != nil ||
			row.OrderCount != nil || row.WaveID != uuid.Nil || row.AssignmentID != uuid.Nil || row.SnapshotID != uuid.Nil || row.TaskID != uuid.Nil ||
			row.PositionCommitID.Valid || row.ParticipantID.Valid || row.Position != nil || len(row.EvidenceDigest) != 0 ||
			row.SubmissionID != nil {
			return progressionGoldenLedgerRevision{}, domain.ErrConflict
		}
		return revision, nil
	}
	attempt, err := progressionGoldenLedgerAttemptFromRow(row)
	if err != nil {
		return progressionGoldenLedgerRevision{}, err
	}
	revision.attempts[attempt.id] = attempt
	if attempt.orderCount == 0 {
		if row.PositionCommitID.Valid || row.ParticipantID.Valid || row.Position != nil || len(row.EvidenceDigest) != 0 || row.SubmissionID != nil {
			return progressionGoldenLedgerRevision{}, domain.ErrConflict
		}
		return revision, nil
	}
	commitment, err := progressionGoldenLedgerCommitmentFromRow(row, attempt.id)
	if err != nil {
		return progressionGoldenLedgerRevision{}, err
	}
	revision.commitments[commitment.commitID] = commitment
	return revision, nil
}

func progressionGoldenLedgerAttemptFromRow(
	row sqlc.LockTournamentProgressionGoldenPositionLedgerRow,
) (progressionGoldenLedgerAttempt, error) {
	if !row.AttemptID.Valid || row.AttemptID.UUID == uuid.Nil || !row.SubmissionRevisionID.Valid ||
		row.SubmissionRevisionID.UUID == uuid.Nil || row.SubmissionRevision == nil || *row.SubmissionRevision < 1 ||
		row.AttemptNumber == nil || *row.AttemptNumber < 1 || row.OrderCount == nil || *row.OrderCount < 0 ||
		row.WaveID == uuid.Nil || row.AssignmentID == uuid.Nil || row.SnapshotID == uuid.Nil || row.TaskID == uuid.Nil {
		return progressionGoldenLedgerAttempt{}, domain.ErrConflict
	}
	return progressionGoldenLedgerAttempt{
		id: row.AttemptID.UUID, submissionRevisionID: row.SubmissionRevisionID.UUID,
		submissionRevision: *row.SubmissionRevision, attemptNo: int(*row.AttemptNumber),
		orderCount: int(*row.OrderCount), waveID: row.WaveID, assignmentID: row.AssignmentID,
		snapshotID: row.SnapshotID, taskID: row.TaskID,
	}, nil
}

func progressionGoldenLedgerCommitmentFromRow(
	row sqlc.LockTournamentProgressionGoldenPositionLedgerRow,
	attemptID uuid.UUID,
) (progressionGoldenLedgerCommitment, error) {
	digest, ok := progressionDigest(row.EvidenceDigest)
	if !ok || !row.PositionCommitID.Valid || row.PositionCommitID.UUID == uuid.Nil || !row.ParticipantID.Valid ||
		row.ParticipantID.UUID == uuid.Nil || row.Position == nil || *row.Position < 1 || row.SubmissionID == nil ||
		*row.SubmissionID < 1 || attemptID == uuid.Nil {
		return progressionGoldenLedgerCommitment{}, domain.ErrConflict
	}
	return progressionGoldenLedgerCommitment{
		commitID: row.PositionCommitID.UUID, attemptID: attemptID, participantID: row.ParticipantID.UUID,
		position: int(*row.Position), submissionID: uint64(*row.SubmissionID), evidenceDigest: digest,
	}, nil
}

func progressionGoldenMergeLedgerRevision(
	left, right progressionGoldenLedgerRevision,
) (progressionGoldenLedgerRevision, error) {
	if left.id != right.id || left.groupID != right.groupID || left.revision != right.revision ||
		!progressionGoldenPreviousEqual(left.previousID, right.previousID) || left.digest != right.digest ||
		!left.finalizedAt.Equal(right.finalizedAt) {
		return progressionGoldenLedgerRevision{}, domain.ErrConflict
	}
	for attemptID, attempt := range right.attempts {
		if existing, found := left.attempts[attemptID]; found && existing != attempt {
			return progressionGoldenLedgerRevision{}, domain.ErrConflict
		}
		left.attempts[attemptID] = attempt
	}
	for commitmentID, commitment := range right.commitments {
		if existing, found := left.commitments[commitmentID]; found && existing != commitment {
			return progressionGoldenLedgerRevision{}, domain.ErrConflict
		}
		left.commitments[commitmentID] = commitment
	}
	return left, nil
}

func progressionGoldenPreviousEqual(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionGoldenLedgerChain(
	chain []progressionGoldenLedgerRevision,
	seals []sqlc.GoldenPositionLedgerRevisionSeal,
	authority tournamentprogression.Authority,
) error {
	if len(chain) == 0 {
		return domain.ErrConflict
	}
	sealByID := make(map[uuid.UUID]sqlc.GoldenPositionLedgerRevisionSeal, len(seals))
	for _, seal := range seals {
		if seal.LedgerRevisionID == uuid.Nil || seal.TournamentID != authority.Tournament.ID ||
			seal.RosterID != authority.Tournament.RosterID || !seal.SealedAt.Valid ||
			!domain.IsValidServerTime(seal.SealedAt.Time.UTC()) {
			return domain.ErrConflict
		}
		if _, duplicate := sealByID[seal.LedgerRevisionID]; duplicate {
			return domain.ErrConflict
		}
		sealByID[seal.LedgerRevisionID] = seal
	}
	for index, revision := range chain {
		if revision.revision != int64(index+1) || (index == 0 && revision.previousID != nil) ||
			(index > 0 && (revision.previousID == nil || *revision.previousID != chain[index-1].id)) {
			return domain.ErrConflict
		}
		seal, found := sealByID[revision.id]
		if !found {
			return domain.ErrConflict
		}
		digest, ok := progressionDigest(seal.PayloadDigest)
		if !ok || digest != revision.digest || seal.SealedAt.Time.UTC().Before(revision.finalizedAt) {
			return domain.ErrConflict
		}
		delete(sealByID, revision.id)
	}
	return nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionGoldenSettlement(
	authority tournamentprogression.Authority,
	group progressionGoldenGroup,
	chain []progressionGoldenLedgerRevision,
) (playoff.Top4GoldenSettlement, error) {
	if len(chain) < 2 {
		return playoff.Top4GoldenSettlement{}, domain.ErrConflict
	}
	final := chain[len(chain)-1]
	if len(final.attempts) != len(chain)-1 || len(final.commitments) != group.to-group.from+1 {
		return playoff.Top4GoldenSettlement{}, domain.ErrConflict
	}
	attempts := make([]playoff.GoldenPositionAttemptEvidence, 0, len(final.attempts))
	for _, attempt := range final.attempts {
		if _, found := group.attempts[attempt.id]; !found {
			return playoff.Top4GoldenSettlement{}, domain.ErrConflict
		}
		attempts = append(attempts, playoff.GoldenPositionAttemptEvidence{
			AttemptID: attempt.id, AttemptNo: attempt.attemptNo, SubmissionRevisionID: attempt.submissionRevisionID,
			SubmissionRevision: attempt.submissionRevision, WaveID: attempt.waveID, AssignmentID: attempt.assignmentID,
			SnapshotID: attempt.snapshotID, TaskID: attempt.taskID, OrderCount: attempt.orderCount,
		})
	}
	slices.SortFunc(attempts, func(left, right playoff.GoldenPositionAttemptEvidence) int {
		return cmp.Compare(left.AttemptNo, right.AttemptNo)
	})
	positions := make([]playoff.GoldenPositionCommitEvidence, 0, len(final.commitments))
	for _, commitment := range final.commitments {
		commit, found := group.commits[commitment.commitID]
		if !found || commit.attemptID != commitment.attemptID || commit.participantID != commitment.participantID ||
			commit.position != commitment.position {
			return playoff.Top4GoldenSettlement{}, domain.ErrConflict
		}
		positions = append(positions, playoff.GoldenPositionCommitEvidence{
			Position: commitment.position, ParticipantID: commitment.participantID, AttemptID: commitment.attemptID,
			AttemptNo: 0, SubmissionID: commitment.submissionID, EvidenceDigest: commitment.evidenceDigest,
			CommitID: commitment.commitID,
		})
	}
	slices.SortFunc(positions, func(left, right playoff.GoldenPositionCommitEvidence) int {
		return cmp.Compare(left.Position, right.Position)
	})
	attemptByID := make(map[uuid.UUID]playoff.GoldenPositionAttemptEvidence, len(attempts))
	for _, attempt := range attempts {
		if _, duplicate := attemptByID[attempt.AttemptID]; duplicate {
			return playoff.Top4GoldenSettlement{}, domain.ErrConflict
		}
		attemptByID[attempt.AttemptID] = attempt
	}
	for index := range positions {
		attempt, found := attemptByID[positions[index].AttemptID]
		if !found {
			return playoff.Top4GoldenSettlement{}, domain.ErrConflict
		}
		positions[index].AttemptNo = attempt.AttemptNo
	}
	finalizedAt := final.finalizedAt.UTC()
	settlementRevisionID := final.id
	if group.settlementRevisionID != uuid.Nil {
		if !domain.IsValidServerTime(group.finalizedAt.UTC()) || group.finalizedAt.UTC() != finalizedAt {
			return playoff.Top4GoldenSettlement{}, domain.ErrConflict
		}
		settlementRevisionID = group.settlementRevisionID
	}
	evidence, err := playoff.NewGoldenPositionEvidence(playoff.GoldenPositionEvidenceInput{
		Scope: goldenusecase.GoldenStateScope{
			TournamentID: authority.Tournament.ID, GroupID: group.id,
			GroupRevisionID: domain.DerivedRevisionID(group.revisionID),
		},
		RevisionID: final.id, Revision: final.revision, PreviousRevisionID: final.previousID,
		RevisionIDs: progressionGoldenRevisionIDs(chain), PositionFrom: group.from, PositionTo: group.to,
		Positions: positions, Attempts: attempts, PayloadDigest: final.digest,
	})
	if err != nil {
		return playoff.Top4GoldenSettlement{}, domain.ErrConflict
	}
	return playoff.Top4GoldenSettlement{
		RevisionID: domain.DerivedRevisionID(settlementRevisionID), RevisionNo: group.revisionNo + 1, Positions: &evidence,
		FinalizedAt: finalizedAt,
	}, nil
}

func progressionGoldenRevisionIDs(chain []progressionGoldenLedgerRevision) []uuid.UUID {
	ids := make([]uuid.UUID, len(chain))
	for index, revision := range chain {
		ids[index] = revision.id
	}
	return ids
}
