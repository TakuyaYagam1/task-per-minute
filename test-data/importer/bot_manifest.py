"""Private runtime catalog consumed by the test player service."""

import json
import os
import secrets
import tempfile
from pathlib import Path


def write_catalog(directory: Path, tasks: list[dict], content_revision: int, control_directory: Path | None = None) -> None:
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    control_directory = control_directory or directory
    control_directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    payload = {"version": 1, "content_revision": content_revision, "tasks": []}
    seen = set()
    for task in tasks:
        identifier = task.get("id")
        version = task.get("version")
        answer = task.get("flag")
        if not identifier or not isinstance(version, int) or version < 1 or not answer:
            raise ValueError("invalid test task identity")
        key = (identifier, version)
        if key in seen:
            raise ValueError("duplicate test task identity")
        seen.add(key)
        payload["tasks"].append({"task_id": identifier, "version": version, "answer": answer, "category": task.get("category"), "kind": task.get("kind")})
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode="w", dir=directory, delete=False) as output:
            temporary = output.name
            os.chmod(temporary, 0o600)
            json.dump(payload, output)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, directory / "tasks.json")
        temporary = None
        key_path = control_directory / "control.key"
        try:
            descriptor = os.open(key_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError:
            if key_path.is_symlink() or key_path.stat().st_mode & 0o777 != 0o600 or len(key_path.read_bytes()) != 64:
                raise ValueError("invalid test control key") from None
        else:
            with os.fdopen(descriptor, "w") as output:
                output.write(secrets.token_hex(32))
                output.flush()
                os.fsync(output.fileno())
        for parent in {directory, control_directory}:
            descriptor = os.open(parent, os.O_RDONLY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
    finally:
        if temporary:
            os.unlink(temporary)
