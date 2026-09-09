package redis

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

const commandReceiptKeyPrefix = "command-receipt:"

var (
	commandReceiptBeginScript = goredis.NewScript(`
local state = redis.call('HGET', KEYS[1], 'state')
if not state then
  redis.call('HSET', KEYS[1], 'state', 'in_flight', 'digest', ARGV[1], 'lease', ARGV[3])
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
if redis.call('HGET', KEYS[1], 'digest') ~= ARGV[1] then
  return -1
end
if state == 'succeeded' then
  return 3
end
if state == 'in_flight' then
  return 2
end
redis.call('HSET', KEYS[1], 'state', 'in_flight', 'lease', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)
	commandReceiptSuccessScript = goredis.NewScript(`
if redis.call('HGET', KEYS[1], 'digest') ~= ARGV[1] then
  return 0
end
if redis.call('HGET', KEYS[1], 'lease') ~= ARGV[2] then
  return 0
end
if redis.call('HGET', KEYS[1], 'state') ~= 'in_flight' then
  return 0
end
redis.call('HSET', KEYS[1], 'state', 'succeeded')
redis.call('HDEL', KEYS[1], 'lease')
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)
	commandReceiptFailureScript = goredis.NewScript(`
if redis.call('HGET', KEYS[1], 'digest') ~= ARGV[1] then
  return 0
end
if redis.call('HGET', KEYS[1], 'lease') ~= ARGV[2] then
  return 0
end
if redis.call('HGET', KEYS[1], 'state') ~= 'in_flight' then
  return 0
end
redis.call('HSET', KEYS[1], 'state', 'failed')
redis.call('HDEL', KEYS[1], 'lease')
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)
)

type CommandReceiptStore struct {
	client       *goredis.Client
	inFlightTTL  time.Duration
	succeededTTL time.Duration
	failedTTL    time.Duration
}

func NewCommandReceiptStore(
	client *goredis.Client,
	inFlightTTL time.Duration,
	succeededTTL time.Duration,
	failedTTL time.Duration,
) *CommandReceiptStore {
	return &CommandReceiptStore{
		client: client, inFlightTTL: inFlightTTL, succeededTTL: succeededTTL, failedTTL: failedTTL,
	}
}

func (s *CommandReceiptStore) Begin(
	ctx context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
) (idempotency.BeginResult, error) {
	if !s.valid() || !validIdempotencyCommand(command) || lease == (idempotency.LeaseToken{}) {
		return idempotency.BeginResult{}, errors.New("command receipt store: invalid begin input")
	}
	result, err := commandReceiptBeginScript.Run(
		ctx,
		s.client,
		[]string{commandReceiptKey(command)},
		hex.EncodeToString(command.PayloadDigest[:]),
		s.inFlightTTL.Milliseconds(),
		hex.EncodeToString(lease[:]),
	).Int()
	if err != nil {
		return idempotency.BeginResult{}, fmt.Errorf("command receipt store begin: %w", err)
	}
	switch result {
	case -1:
		return idempotency.BeginResult{}, idempotency.ErrPayloadConflict
	case 1:
		return idempotency.BeginResult{Disposition: idempotency.BeginAcquired, Lease: lease}, nil
	case 2:
		return idempotency.BeginResult{Disposition: idempotency.BeginInFlight}, nil
	case 3:
		return idempotency.BeginResult{Disposition: idempotency.BeginSucceeded}, nil
	default:
		return idempotency.BeginResult{}, errors.New("command receipt store: unexpected begin result")
	}
}

func (s *CommandReceiptStore) MarkSucceeded(
	ctx context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
) error {
	return s.transition(ctx, command, lease, commandReceiptSuccessScript, s.succeededTTL, "succeed")
}

func (s *CommandReceiptStore) MarkFailed(
	ctx context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
) error {
	return s.transition(ctx, command, lease, commandReceiptFailureScript, s.failedTTL, "fail")
}

func (s *CommandReceiptStore) transition(
	ctx context.Context,
	command idempotency.Command,
	lease idempotency.LeaseToken,
	script *goredis.Script,
	ttl time.Duration,
	operation string,
) error {
	if !s.valid() || !validIdempotencyCommand(command) || lease == (idempotency.LeaseToken{}) {
		return fmt.Errorf("command receipt store: invalid %s input", operation)
	}
	changed, err := script.Run(
		ctx,
		s.client,
		[]string{commandReceiptKey(command)},
		hex.EncodeToString(command.PayloadDigest[:]),
		hex.EncodeToString(lease[:]),
		ttl.Milliseconds(),
	).Int()
	if err != nil {
		return fmt.Errorf("command receipt store %s: %w", operation, err)
	}
	if changed != 1 {
		return fmt.Errorf("command receipt store: rejected %s transition: %w", operation, idempotency.ErrLeaseLost)
	}
	return nil
}

func (s *CommandReceiptStore) valid() bool {
	return s != nil && s.client != nil && s.inFlightTTL.Milliseconds() >= 1 &&
		s.succeededTTL.Milliseconds() >= 1 && s.failedTTL.Milliseconds() >= 1
}

func validIdempotencyCommand(command idempotency.Command) bool {
	return command.ID != [16]byte{} && command.PayloadDigest != [32]byte{} &&
		command.Namespace == strings.TrimSpace(command.Namespace) && command.Namespace != "" &&
		len(command.Namespace) <= 64 && !strings.ContainsAny(command.Namespace, ":\x00")
}

func commandReceiptKey(command idempotency.Command) string {
	return commandReceiptKeyPrefix + command.Namespace + ":" + command.ID.String()
}

var _ idempotency.Store = (*CommandReceiptStore)(nil)
