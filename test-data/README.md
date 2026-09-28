# Test task fixture stack

This directory contains twelve packaged tasks, ten generated easy tasks, and
the existing five web task runtimes. Challenge flags, hints,
writeups, solver material, runtime databases, caches, and dependencies stay
local and are excluded by the repository ignore rules.

Before starting, provide `admin/flag.txt` for each of the twelve packaged
tasks. These files are required read-only mounts. A clean clone needs them
supplied separately; Compose will stop if any file is missing. The ten easy
tasks derive their answers and downloadable puzzles from a private seed created
in the `test-task-seed` Docker volume. The seed and answers are never committed.
Keep this volume together with the test database when restarting the stack;
removing only the seed would change the generated task answers.

Start the complete stack with the existing production environment values:

```text
docker compose --env-file .env -f deployment/docker/docker-compose.test.yml up --build
```

The regular stack files and behavior remain unchanged. Test mode is a separate
stack that uses the same host-facing application and Caddy ports, so start it
when those ports are available on the host.

The compose file keeps the app and task services on isolated internal
networks. Caddy routes the five task domains to the existing task service
names. The task domains can be overridden with `TASK_*_URL` values; otherwise
the importer builds HTTPS URLs from the existing `TASK_*_DOMAIN` values. Its
one-shot importer logs in through the admin API, creates or reuses all 22
tasks without overwriting changed tasks, uploads public source archives to
object storage, and verifies a published tournament content revision. The
admin password is supplied through the environment.

The fixture contains 18 normal and four Golden tasks. Its default stage
categories need nine normal Crypto, three normal Reverse, three normal Web,
one normal Forensics, one normal Pwn, and two Golden Crypto tasks for a
four-player tournament with zero reserves. The remaining tasks stay available
for other configurations. With these stage categories and zero reserves, the
capacity proof needs 19 tasks for four players, 27 for eight, or 51 for sixteen.
This fixture supplies the four-player case. Changing stage categories or adding
reserves changes the required distribution.
