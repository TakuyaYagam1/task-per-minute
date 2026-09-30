import { execFileSync } from 'node:child_process';

const accountFixturePassword = 'synthetic-fixture-password-123';
const accountFixturePasswordHash = '$argon2id$v=19$m=19456,t=2,p=1$Zml4dHVyZS1zYWx0LTAxMg$2NqFe86kZ4nitnwiIAS6jmW9775VGabqe8Cgouvct5o';
const containerIDPattern = /^[a-f0-9]{64}$/i;
const projectNamePattern = /^[a-z0-9][a-z0-9_-]{2,62}$/;
const usernamePattern = /^[a-z0-9][a-z0-9_-]{1,49}$/;

const requireFixtureTarget = (): { containerID: string; project: string } => {
  if (process.env.E2E_FULL_STACK_ISOLATED !== '1') {
    throw new Error('Synthetic account seeding requires the isolated full-stack runner');
  }

  const containerID = process.env.E2E_ACCOUNT_FIXTURE_POSTGRES_CONTAINER_ID ?? '';
  const project = process.env.E2E_ACCOUNT_FIXTURE_COMPOSE_PROJECT ?? '';
  if (!containerIDPattern.test(containerID) || !projectNamePattern.test(project) || project === 'task-per-minute-e2e') {
    throw new Error('Synthetic account seeding requires a validated isolated PostgreSQL target');
  }

  let inspected: string;
  try {
    inspected = execFileSync('docker', [
      'inspect',
      '--format',
      '{{.Id}}|{{.State.Running}}|{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}',
      containerID,
    ], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
      timeout: 10_000,
    }).trim();
  } catch {
    throw new Error('Synthetic account seeding could not verify its PostgreSQL container');
  }

  const [actualID, running, actualProject, service, ...unexpected] = inspected.split('|');
  if (
    unexpected.length > 0
    || actualID !== containerID
    || running !== 'true'
    || actualProject !== project
    || service !== 'postgres'
  ) {
    throw new Error('Synthetic account seeding refused an unowned PostgreSQL container');
  }

  return { containerID, project };
};

export const seedVerifiedAccount = (username: string): string => {
  if (!usernamePattern.test(username)) {
    throw new Error('Synthetic account username must be lowercase and use safe fixture characters');
  }

  const { containerID } = requireFixtureTarget();
  const email = `${username}@example.invalid`;
  const sql = `
BEGIN;
WITH fixture_player AS (
  INSERT INTO players (username)
  VALUES ('${username}')
  ON CONFLICT (username) DO UPDATE
    SET username = EXCLUDED.username
    WHERE players.deleted_at IS NULL
  RETURNING id
), fixture_account AS (
  INSERT INTO player_accounts (
    player_id,
    username,
    username_normalized,
    email,
    email_normalized,
    password_hash,
    email_verified_at
  )
  SELECT
    id,
    '${username}',
    '${username}',
    '${email}',
    '${email}',
    '${accountFixturePasswordHash}',
    now()
  FROM fixture_player
  ON CONFLICT (username_normalized) DO UPDATE
    SET player_id = EXCLUDED.player_id,
        username = EXCLUDED.username,
        email = EXCLUDED.email,
        email_normalized = EXCLUDED.email_normalized,
        password_hash = EXCLUDED.password_hash,
        verification_token_hash = NULL,
        verification_expires_at = NULL,
        verification_sent_at = NULL,
        email_verified_at = EXCLUDED.email_verified_at
    WHERE player_accounts.player_id = EXCLUDED.player_id
      AND player_accounts.email_normalized = EXCLUDED.email_normalized
      AND player_accounts.email_verified_at IS NOT NULL
  RETURNING id, username_normalized
)
INSERT INTO player_username_reservations (normalized_username, legacy_count, account_id)
SELECT username_normalized, 0, id
FROM fixture_account
ON CONFLICT (normalized_username) DO UPDATE
  SET account_id = EXCLUDED.account_id
  WHERE player_username_reservations.legacy_count = 0
    AND (player_username_reservations.account_id IS NULL
         OR player_username_reservations.account_id = EXCLUDED.account_id);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM player_accounts AS account
    JOIN players AS player ON player.id = account.player_id
    JOIN player_username_reservations AS reservation
      ON reservation.normalized_username = account.username_normalized
     AND reservation.account_id = account.id
     AND reservation.legacy_count = 0
    WHERE account.username_normalized = '${username}'
      AND account.email_normalized = '${email}'
      AND account.password_hash = '${accountFixturePasswordHash}'
      AND account.email_verified_at IS NOT NULL
      AND player.deleted_at IS NULL
  ) THEN
    RAISE EXCEPTION 'synthetic account fixture could not be verified';
  END IF;
END
$$;
COMMIT;
`;

  try {
    execFileSync('docker', [
      'exec',
      '-i',
      containerID,
      'sh',
      '-c',
      'exec psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"',
    ], {
      encoding: 'utf8',
      input: sql,
      stdio: ['pipe', 'ignore', 'ignore'],
      timeout: 15_000,
    });
  } catch {
    throw new Error('Synthetic account seeding failed for the isolated PostgreSQL fixture');
  }

  return accountFixturePassword;
};
