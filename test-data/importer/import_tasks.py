"""Import the public test task catalog through the admin API.

The importer deliberately uses the same HTTP API as the admin UI. It reads
fixture flags only in memory and never includes task bodies, flags, cookies, or
API responses in its output.
"""

from __future__ import annotations

import io
import json
import os
import sys
import uuid
import zipfile
from http import cookiejar
from pathlib import Path
from urllib import error, request

from easy_tasks import generated_specs


class ImportFailure(RuntimeError):
    """A user-actionable fixture import failure without response-body details."""


MANIFEST = (
    ("web", "task01-cookie", 3001, "TASK_COOKIE_URL", "golden"),
    ("web", "task02-robots", 3002, "TASK_ARCHIVE_URL", "normal"),
    ("web", "task03-status", 3003, "TASK_SAMOVAR_URL", "normal"),
    ("web", "task04-sqli", 3004, "TASK_VKONTAKTE_URL", "normal"),
    ("web", "task05-xss", 3005, "TASK_DEDYS_URL", "normal"),
    ("crypto", "task06-caesar", None, None, "golden"),
    ("crypto", "task07-xor", None, None, "normal"),
    ("crypto", "task08-atbash", None, None, "normal"),
    ("reverse", "task09-js-checker", None, None, "golden"),
    ("reverse", "task10-bytecode", None, None, "normal"),
    ("forensics", "task11-png-comment", None, None, "normal"),
    ("pwn", "task12-stack-bytes", None, None, "normal"),
)

API_TIMEOUT_SECONDS = 30


def env_required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise ImportFailure(f"missing required environment variable: {name}")
    return value


def read_fixture_text(path: Path, label: str) -> str:
    try:
        value = path.read_text(encoding="utf-8").strip()
    except (OSError, UnicodeError) as exc:
        raise ImportFailure(f"unable to read fixture {label}") from exc
    if not value:
        raise ImportFailure(f"fixture {label} is empty")
    return value


def build_source_archive(user_root: Path) -> bytes:
    archive = io.BytesIO()
    try:
        with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
            files = sorted(
                path
                for path in user_root.rglob("*")
                if path.is_file() and not path.is_symlink()
            )
            if not files:
                raise ImportFailure("fixture user directory has no source files")
            for path in files:
                relative = path.relative_to(user_root)
                entry = zipfile.ZipInfo(relative.as_posix(), date_time=(2020, 1, 1, 0, 0, 0))
                entry.create_system = 3
                entry.external_attr = 0o100644 << 16
                entry.compress_type = zipfile.ZIP_DEFLATED
                output.writestr(entry, path.read_bytes())
    except (OSError, ValueError, zipfile.BadZipFile) as exc:
        raise ImportFailure("unable to build source archive") from exc
    return archive.getvalue()


def fixture_specs(
    root: Path,
    difficulty: str,
    time_limit: int,
    web_scheme: str,
) -> list[dict]:
    specs = []
    for category, slug, web_port, url_env, kind in MANIFEST:
        task_root = root / category / slug
        user_root = task_root / "user"
        title = read_fixture_text(user_root / "name.txt", f"{slug} title")
        description = read_fixture_text(user_root / "description.txt", f"{slug} description")
        fixture_category = read_fixture_text(user_root / "category.txt", f"{slug} category")
        if fixture_category != category:
            raise ImportFailure(f"fixture category mismatch for {slug}")

        # Keep the flag in memory only. It is required by the admin API but is
        # deliberately never printed or included in generated source archives.
        flag = read_fixture_text(task_root / "admin" / "flag.txt", f"{slug} flag")
        source_archive = build_source_archive(user_root)
        task_url = None
        if web_port is not None:
            task_url = os.environ.get(url_env or "", "").strip()
            if not task_url:
                domain_env = (url_env or "").removesuffix("_URL") + "_DOMAIN"
                domain = os.environ.get(domain_env, "").strip()
                if not domain:
                    raise ImportFailure(f"missing public domain for {slug}")
                task_url = f"{web_scheme}://{domain}"
            if not task_url.startswith(("http://", "https://")):
                raise ImportFailure(f"{slug} task URL must be an HTTP URL")

        specs.append(
            {
                "slug": slug,
                "title": title,
                "description": description,
                "category": category,
                "difficulty": difficulty,
                "time_limit": time_limit,
                "flag": flag,
                "kind": kind,
                "enabled": True,
                "task_url": task_url,
                "source_archive": source_archive,
            }
        )
    return specs


class AdminAPI:
    def __init__(self, base_url: str, password: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.password = password
        self.csrf_token = ""
        self.opener = request.build_opener(request.HTTPCookieProcessor(cookiejar.CookieJar()))

    def call(
        self,
        method: str,
        path: str,
        *,
        body: bytes | None = None,
        content_type: str | None = None,
        csrf: bool = False,
    ) -> tuple[int, dict | list | None, object]:
        headers = {"Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = content_type or "application/json"
        if csrf:
            if not self.csrf_token:
                raise ImportFailure("admin CSRF token is unavailable")
            headers["X-CSRF-Token"] = self.csrf_token
        url = f"{self.base_url}{path}"
        req = request.Request(url, data=body, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=API_TIMEOUT_SECONDS) as response:
                raw = response.read()
                status = response.status
                response_headers = response.headers
        except error.HTTPError as exc:
            raise ImportFailure(f"admin API returned HTTP {exc.code} for {method} {path}") from exc
        except (error.URLError, TimeoutError, OSError) as exc:
            raise ImportFailure(f"admin API request failed for {method} {path}") from exc

        refreshed_csrf = response_headers.get("X-CSRF-Token", "")
        if refreshed_csrf:
            self.csrf_token = refreshed_csrf
        if not raw:
            return status, None, response_headers
        try:
            return status, json.loads(raw.decode("utf-8")), response_headers
        except (UnicodeError, json.JSONDecodeError) as exc:
            raise ImportFailure(f"admin API returned invalid JSON for {method} {path}") from exc

    def login(self) -> None:
        payload = json.dumps({"password": self.password}).encode("utf-8")
        status, _, headers = self.call("POST", "/api/v1/admin/login", body=payload)
        if status != 200:
            raise ImportFailure("admin login failed")
        self.csrf_token = headers.get("X-CSRF-Token", "")
        if not self.csrf_token:
            raise ImportFailure("admin login did not return a CSRF token")

    def list_tasks(self) -> list[dict]:
        status, payload, _ = self.call("GET", "/api/v1/admin/tasks")
        if status != 200 or not isinstance(payload, list):
            raise ImportFailure("admin task list is unavailable")
        return payload

    def create_task(self, spec: dict) -> dict:
        payload = {
            key: spec[key]
            for key in (
                "title",
                "description",
                "category",
                "difficulty",
                "time_limit",
                "flag",
                "kind",
                "enabled",
                "task_url",
            )
            if spec[key] is not None
        }
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        status, response_payload, _ = self.call(
            "POST", "/api/v1/admin/tasks", body=body, csrf=True
        )
        if status != 201 or not isinstance(response_payload, dict):
            raise ImportFailure("admin task creation failed")
        return response_payload

    def upload_source(self, task_id: str, source_archive: bytes) -> None:
        boundary = f"----task-per-minute-{uuid.uuid4().hex}"
        body = (
            f"--{boundary}\r\n"
            'Content-Disposition: form-data; name="file"; filename="source.zip"\r\n'
            "Content-Type: application/zip\r\n\r\n"
        ).encode("ascii")
        body += source_archive
        body += f"\r\n--{boundary}--\r\n".encode("ascii")
        status, _, _ = self.call(
            "POST",
            f"/api/v1/admin/tasks/{task_id}/source",
            body=body,
            content_type=f"multipart/form-data; boundary={boundary}",
            csrf=True,
        )
        if status != 200:
            raise ImportFailure("source archive upload failed")

    def tournament_content(self) -> dict:
        status, payload, _ = self.call("GET", "/api/v1/admin/tournament-content")
        if status != 200 or not isinstance(payload, dict):
            raise ImportFailure("published tournament content is unavailable")
        required = (
            "content_revision",
            "publication_id",
            "normal_pool_revision_id",
            "golden_pool_revision_id",
        )
        if any(not payload.get(key) for key in required):
            raise ImportFailure("published tournament content is incomplete")
        if payload["normal_pool_revision_id"] == payload["golden_pool_revision_id"]:
            raise ImportFailure("published tournament pools are not distinct")
        return payload


def same_fixture_task(task: dict, spec: dict) -> bool:
    fields = (
        "title",
        "description",
        "category",
        "difficulty",
        "time_limit",
        "flag",
        "kind",
        "enabled",
        "task_url",
    )
    return all(task.get(field) == spec[field] for field in fields)


def import_specs(api: AdminAPI, specs: list[dict]) -> tuple[int, int]:
    existing = api.list_tasks()
    imported = 0
    reused = 0
    for spec in specs:
        title_matches = [task for task in existing if task.get("title") == spec["title"]]
        if len(title_matches) > 1:
            raise ImportFailure(f"ambiguous existing task title for {spec['slug']}")
        if title_matches:
            task = title_matches[0]
            if not same_fixture_task(task, spec):
                raise ImportFailure(f"existing task differs from fixture for {spec['slug']}")
            task_id = task.get("id")
            if not task_id:
                raise ImportFailure(f"existing task has no id for {spec['slug']}")
            reused += 1
            if task.get("source_file_url") is None:
                api.upload_source(task_id, spec["source_archive"])
            continue

        task = api.create_task(spec)
        task_id = task.get("id")
        if not task_id:
            raise ImportFailure(f"created task has no id for {spec['slug']}")
        api.upload_source(task_id, spec["source_archive"])
        imported += 1
        existing.append(task)
    return imported, reused


def run() -> None:
    root = Path(os.environ.get("TEST_DATA_ROOT", "/test-data")).resolve()
    base_url = os.environ.get("BASE_URL", "http://backend:8080").strip()
    password = env_required("ADMIN_PASSWORD")
    difficulty = os.environ.get("TEST_TASK_DIFFICULTY", "medium").strip()
    try:
        time_limit = int(os.environ.get("TEST_TASK_TIME_LIMIT", "180"))
    except ValueError as exc:
        raise ImportFailure("TEST_TASK_TIME_LIMIT must be an integer") from exc
    if difficulty not in {"easy", "medium", "hard"} or time_limit < 1:
        raise ImportFailure("invalid test task defaults")

    web_scheme = os.environ.get("TASK_WEB_SCHEME", "https").strip().lower()
    if web_scheme not in {"http", "https"}:
        raise ImportFailure("TASK_WEB_SCHEME must be http or https")
    specs = fixture_specs(root, difficulty, time_limit, web_scheme)
    seed_file = Path(os.environ.get("TEST_TASK_SEED_FILE", "/seed/key"))
    try:
        seed = seed_file.read_bytes()
        specs.extend(generated_specs(seed, time_limit))
    except (OSError, ValueError) as exc:
        raise ImportFailure("unable to load generated test tasks") from exc
    api = AdminAPI(base_url, password)
    api.login()
    imported, reused = import_specs(api, specs)
    api.tournament_content()
    print(f"test task import ready: {len(specs)} tasks ({imported} created, {reused} reused)")


if __name__ == "__main__":
    try:
        run()
    except ImportFailure as exc:
        print(f"test task import failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from None
