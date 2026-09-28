"""Create one private seed for generated local test tasks."""

from __future__ import annotations

import os
from pathlib import Path


seed_file = Path("/seed/key")
try:
    descriptor = os.open(seed_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
except FileExistsError:
    if seed_file.stat().st_size != 32:
        raise SystemExit("test task seed has an invalid size") from None
else:
    with os.fdopen(descriptor, "wb") as output:
        output.write(os.urandom(32))
        output.flush()
        os.fsync(output.fileno())

# Only the seed service and importer mount this volume. The importer runs as
# a different user, so the root-owned file must be readable inside that mount.
seed_file.chmod(0o644)
print("test task seed ready")
