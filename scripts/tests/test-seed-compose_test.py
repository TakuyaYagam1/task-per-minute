"""Static checks for the test-only account seed image and Compose wiring."""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]


def top_level_block(text: str, key: str, indent: int = 2) -> str:
    """Return one indentation-based YAML mapping block without parsing env files."""
    lines = text.splitlines()
    marker = f"{' ' * indent}{key}:"
    start = next((index for index, line in enumerate(lines) if line == marker), None)
    if start is None:
        raise AssertionError(f"missing YAML mapping {key!r}")

    end = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line.strip() and len(line) - len(line.lstrip()) <= indent:
            end = index
            break
    return "\n".join(lines[start:end])


class TestSeedComposeTest(unittest.TestCase):
    def test_bot_service_has_only_private_volumes_and_no_public_port(self) -> None:
        compose = (ROOT / "deployment/docker/docker-compose.test.yml").read_text()
        service = top_level_block(compose, "test-bots")
        self.assertNotIn("ports:", service)
        self.assertIn("networks: [internal]", service)
        for mount in ("test-account-seed:/accounts:ro", "test-bot-catalog:/catalog:ro", "test-bot-control:/control:ro", "test-bot-state:/state"):
            self.assertIn(mount, service)
        self.assertEqual(service.count("condition: service_completed_successfully"), 2)
        gateway = (ROOT / "deployment/docker/docker-compose.test.backend.yml").read_text()
        backend = top_level_block(gateway, "backend")
        self.assertIn("target: test-backend", backend)
        self.assertNotIn("test-bot-catalog:", backend)
        self.assertNotIn("test-account-seed:", backend)
        dockerfile = (ROOT / "backend/Dockerfile").read_text()
        self.assertIn("-tags=testtools", dockerfile)
        self.assertTrue(dockerfile.rstrip().endswith("FROM runtime-base AS runtime"))

    def test_server_seed_service_is_isolated_and_automatic(self) -> None:
        compose = (ROOT / "deployment/docker/docker-compose.test.yml").read_text()
        service = top_level_block(compose, "test-accounts")
        importer = top_level_block(compose, "test-data-importer")
        task_seed = top_level_block(compose, "test-data-seed")

        for expected in (
            "target: account-seed",
            'TEST_ACCOUNTS_ENABLED: "true"',
            "DB_DSN: ${DB_DSN:?DB_DSN is required}",
            "TEST_ACCOUNTS_EXPECTED_DB: ${POSTGRES_DB:?POSTGRES_DB is required}",
            "TEST_ACCOUNTS_FILE: /seed/accounts.json",
            "read_only: true",
            "no-new-privileges:true",
            "cap_drop:",
            "- ALL",
            "pids_limit: 64",
            'cpus: "0.5"',
            "memory: 256M",
            "pids: 64",
            "- internal",
            "- test-account-seed:/seed",
            "condition: service_healthy",
            'restart: "no"',
        ):
            with self.subTest(expected=expected):
                self.assertIn(expected, service)

        self.assertNotIn("profiles:", service)
        self.assertNotIn("ports:", service)
        self.assertNotIn("profiles:", importer)
        self.assertIn("source: test-task-seed", importer)
        self.assertIn("network_mode: none", task_seed)
        self.assertRegex(compose, r"(?m)^  test-account-seed:\s*$")

    def test_local_seed_uses_local_network_and_importer_is_automatic(self) -> None:
        compose = (ROOT / "deployment/docker/docker-compose.local.test.yml").read_text()
        seed_service = top_level_block(compose, "test-accounts")
        importer_service = top_level_block(compose, "test-data-importer")

        self.assertIn("service: test-accounts", seed_service)
        self.assertIn("networks: !override\n      - default", seed_service)
        self.assertNotIn("profiles:", seed_service)
        self.assertNotIn("profiles:", importer_service)
        self.assertRegex(compose, r"(?m)^  test-account-seed:\s*$")

    def test_seed_image_is_separate_from_default_runtime(self) -> None:
        dockerfile = (ROOT / "backend/Dockerfile").read_text()
        stages = re.findall(r"(?m)^FROM .+ AS (\S+)\s*$", dockerfile)

        self.assertIn("account-seed-builder", stages)
        self.assertIn("account-seed", stages)
        self.assertEqual(stages[-1], "runtime")
        self.assertLess(stages.index("account-seed"), stages.index("runtime"))

        seed_image = dockerfile.split("FROM alpine:3.23 AS account-seed\n", 1)[1].split(
            "\nFROM alpine:3.23 AS runtime", 1
        )[0]
        self.assertIn("COPY --from=account-seed-builder", seed_image)
        self.assertIn("mkdir -p /app /seed", seed_image)
        self.assertIn("chown 1001:1001 /app /seed", seed_image)
        self.assertIn("chmod 0700 /seed", seed_image)
        self.assertIn("USER 1001:1001", seed_image)
        self.assertNotIn("COPY --from=account-seed-builder", dockerfile.split("FROM alpine:3.23 AS runtime-base\n", 1)[1])


if __name__ == "__main__":
    unittest.main()
