package arena

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/taskexec"
)

var (
	ErrTaskIneligible               = errors.New("arena task version is ineligible")
	ErrInvalidImmutableTaskSnapshot = errors.New("invalid immutable Arena task snapshot")
)

type TaskValidationPolicy string

const TaskValidationPolicyServerExactFlagV1 TaskValidationPolicy = "server_exact_flag_v1"

type TaskInstanceInput struct {
	ParticipantID  uuid.UUID
	InstanceID     uuid.UUID
	RuntimeProfile string
}

type TaskInstanceDescriptor struct {
	ParticipantID    uuid.UUID
	InstanceID       uuid.UUID
	RuntimeProfile   string
	ContentDigest    [sha256.Size]byte
	ValidationPolicy TaskValidationPolicy
	DeadlineSeconds  int
}

type ImmutableTaskSnapshotInput struct {
	SnapshotID  uuid.UUID
	Kind        domain.ArenaTaskKind
	Eligibility TaskEligibilityInput
	Instances   [2]TaskInstanceInput
}

type ImmutableTaskSnapshot struct {
	snapshot      domain.ArenaTaskSnapshot
	contentDigest [sha256.Size]byte
	instances     [2]TaskInstanceDescriptor
}

func BuildImmutableTaskSnapshot(input ImmutableTaskSnapshotInput) (ImmutableTaskSnapshot, error) {
	decision, err := EvaluateTaskEligibility(input.Eligibility)
	if err != nil {
		return ImmutableTaskSnapshot{}, immutableTaskSnapshotError("eligibility: %v", err)
	}
	if !decision.Eligible {
		return ImmutableTaskSnapshot{}, fmt.Errorf("%w: %v", ErrTaskIneligible, decision.Reasons)
	}
	if input.SnapshotID == uuid.Nil || input.Kind != input.Eligibility.Pool.Kind {
		return ImmutableTaskSnapshot{}, immutableTaskSnapshotError("invalid snapshot identity or kind")
	}
	snapshot, err := taskexec.BuildSnapshot(taskexec.SnapshotInput{
		SnapshotID: input.SnapshotID,
		Version:    input.Eligibility.Candidate.Version,
		Kind:       input.Kind,
		Task:       input.Eligibility.Candidate.Task,
	})
	if err != nil {
		return ImmutableTaskSnapshot{}, immutableTaskSnapshotError("build content: %v", err)
	}
	digest, err := taskexec.SnapshotDigest(snapshot)
	if err != nil {
		return ImmutableTaskSnapshot{}, immutableTaskSnapshotError("content digest: %v", err)
	}
	instances, err := buildTaskInstanceDescriptors(input, digest, snapshot.TimeLimit)
	if err != nil {
		return ImmutableTaskSnapshot{}, err
	}
	built := ImmutableTaskSnapshot{
		snapshot: snapshot, contentDigest: digest, instances: instances,
	}
	if err := built.Validate(); err != nil {
		return ImmutableTaskSnapshot{}, err
	}
	return built, nil
}

func (s ImmutableTaskSnapshot) Validate() error {
	if err := s.snapshot.Validate(); err != nil {
		return immutableTaskSnapshotError("content: %v", err)
	}
	digest, err := taskexec.SnapshotDigest(s.snapshot)
	if err != nil || digest != s.contentDigest {
		return immutableTaskSnapshotError("content digest does not match")
	}
	return validateImmutableTaskInstances(s)
}

func validateImmutableTaskInstances(s ImmutableTaskSnapshot) error {
	if s.instances[0].ParticipantID == s.instances[1].ParticipantID ||
		s.instances[0].InstanceID == s.instances[1].InstanceID {
		return immutableTaskSnapshotError("instances are not isolated")
	}
	for _, instance := range s.instances {
		if instance.ParticipantID == uuid.Nil || instance.InstanceID == uuid.Nil ||
			instance.RuntimeProfile == "" || instance.ContentDigest != s.contentDigest ||
			instance.ValidationPolicy != TaskValidationPolicyServerExactFlagV1 ||
			instance.DeadlineSeconds != s.snapshot.TimeLimit {
			return immutableTaskSnapshotError("invalid instance descriptor")
		}
	}
	if s.instances[0].RuntimeProfile != s.instances[1].RuntimeProfile ||
		s.instances[0].ValidationPolicy != s.instances[1].ValidationPolicy ||
		s.instances[0].DeadlineSeconds != s.instances[1].DeadlineSeconds {
		return immutableTaskSnapshotError("participant instances are not functionally equivalent")
	}
	return nil
}

func (s ImmutableTaskSnapshot) Snapshot() domain.ArenaTaskSnapshot {
	return cloneTaskSnapshot(s.snapshot)
}

func (s ImmutableTaskSnapshot) ContentDigest() [sha256.Size]byte {
	return s.contentDigest
}

func (s ImmutableTaskSnapshot) InstanceFor(
	participantID uuid.UUID,
) (TaskInstanceDescriptor, bool) {
	for _, instance := range s.instances {
		if instance.ParticipantID == participantID {
			return instance, true
		}
	}
	return TaskInstanceDescriptor{}, false
}

func (s ImmutableTaskSnapshot) Instances() [2]TaskInstanceDescriptor {
	return s.instances
}

func buildTaskInstanceDescriptors(
	input ImmutableTaskSnapshotInput,
	digest [sha256.Size]byte,
	deadlineSeconds int,
) ([2]TaskInstanceDescriptor, error) {
	participants, err := normalizeExactNormalParticipants(input.Eligibility.ParticipantIDs)
	if err != nil {
		return [2]TaskInstanceDescriptor{}, immutableTaskSnapshotError("participants: %v", err)
	}
	instanceInputs := input.Instances
	sort.Slice(instanceInputs[:], func(i, j int) bool {
		return bytes.Compare(instanceInputs[i].ParticipantID[:], instanceInputs[j].ParticipantID[:]) < 0
	})
	for index := range instanceInputs {
		instanceInputs[index].RuntimeProfile = strings.TrimSpace(instanceInputs[index].RuntimeProfile)
		if instanceInputs[index].ParticipantID != participants[index] ||
			instanceInputs[index].InstanceID == uuid.Nil || instanceInputs[index].RuntimeProfile == "" {
			return [2]TaskInstanceDescriptor{}, immutableTaskSnapshotError("invalid instance identity")
		}
	}
	if instanceInputs[0].InstanceID == instanceInputs[1].InstanceID ||
		instanceInputs[0].RuntimeProfile != instanceInputs[1].RuntimeProfile {
		return [2]TaskInstanceDescriptor{}, immutableTaskSnapshotError("instances are shared or not equivalent")
	}
	var result [2]TaskInstanceDescriptor
	for index, instance := range instanceInputs {
		result[index] = TaskInstanceDescriptor{
			ParticipantID: instance.ParticipantID, InstanceID: instance.InstanceID,
			RuntimeProfile: instance.RuntimeProfile, ContentDigest: digest,
			ValidationPolicy: TaskValidationPolicyServerExactFlagV1,
			DeadlineSeconds:  deadlineSeconds,
		}
	}
	return result, nil
}

func cloneTaskSnapshot(snapshot domain.ArenaTaskSnapshot) domain.ArenaTaskSnapshot {
	clone := snapshot
	clone.Hints = append([]string(nil), snapshot.Hints...)
	clone.TaskURL = cloneArenaStringPointer(snapshot.TaskURL)
	clone.SourceFileURL = cloneArenaStringPointer(snapshot.SourceFileURL)
	return clone
}

func cloneArenaStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func immutableTaskSnapshotError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidImmutableTaskSnapshot, fmt.Sprintf(format, arguments...))
}
