"""Offline checks for the generated task catalog."""

from __future__ import annotations

import ast
import base64
import binascii
import codecs
import gzip
import hashlib
import io
import json
import quopri
import re
import unittest
import zlib
import zipfile
from collections import Counter
from pathlib import Path

import easy_tasks


ORIGINAL_TEN_FINGERPRINT = "fc9aa4d587d894a1a7dce6ef945b81d428a3a3828499033603df276847e6ead0"


def _catalog_fingerprint(specs: list[dict]) -> str:
    projection = []
    for spec in specs:
        projection.append(
            {
                key: hashlib.sha256(value).hexdigest() if key == "source_archive" else value
                for key, value in spec.items()
            }
        )
    canonical = json.dumps(
        projection, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")
    return hashlib.sha256(canonical).hexdigest()


def _manifest_from_source() -> tuple[tuple, ...]:
    source_path = Path(__file__).with_name("import_tasks.py")
    tree = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(source_path))
    for node in tree.body:
        if isinstance(node, ast.Assign) and any(
            isinstance(target, ast.Name) and target.id == "MANIFEST" for target in node.targets
        ):
            return ast.literal_eval(node.value)
    raise AssertionError("importer MANIFEST assignment was not found")


def _unshift(value: str, shift: int) -> str:
    result = []
    for char in value:
        if "a" <= char <= "z":
            result.append(chr((ord(char) - ord("a") + shift) % 26 + ord("a")))
        elif "A" <= char <= "Z":
            result.append(chr((ord(char) - ord("A") + shift) % 26 + ord("A")))
        else:
            result.append(char)
    return "".join(result)


def _decrypt_vigenere(value: str, key: str) -> str:
    result = []
    key_index = 0
    for char in value:
        if "a" <= char <= "z" or "A" <= char <= "Z":
            base = ord("a") if char.islower() else ord("A")
            shift = ord(key[key_index % len(key)].lower()) - ord("a")
            result.append(chr((ord(char) - base - shift) % 26 + base))
            key_index += 1
        else:
            result.append(char)
    return "".join(result)


def _decrypt_affine(value: str) -> str:
    result = []
    inverse = pow(5, -1, 26)
    for char in value:
        if "a" <= char <= "z":
            result.append(chr((inverse * (ord(char) - ord("a") - 8)) % 26 + ord("a")))
        elif "A" <= char <= "Z":
            result.append(chr((inverse * (ord(char) - ord("A") - 8)) % 26 + ord("A")))
        else:
            result.append(char)
    return "".join(result)


def _undo_rot47(value: str) -> str:
    return "".join(
        chr(33 + ((ord(char) - 33 - 47) % 94)) if "!" <= char <= "~" else char
        for char in value
    )


def _xor_repeating(data: bytes, key: bytes) -> bytes:
    return bytes(byte ^ key[index % len(key)] for index, byte in enumerate(data))


def _unrail_fence(value: str, rails: int) -> str:
    pattern = []
    row = 0
    direction = 1
    for _ in value:
        pattern.append(row)
        if row == 0:
            direction = 1
        elif row == rails - 1:
            direction = -1
        row += direction

    counts = [pattern.count(index) for index in range(rails)]
    fences = []
    offset = 0
    for count in counts:
        fences.append(list(value[offset : offset + count]))
        offset += count

    positions = [0] * rails
    decoded = []
    for fence_row in pattern:
        decoded.append(fences[fence_row][positions[fence_row]])
        positions[fence_row] += 1
    return "".join(decoded)


def _uncolumnar(value: str, key: str, original_length: int) -> str:
    width = len(key)
    row_count = (original_length + width - 1) // width
    column_lengths = [
        sum(1 for row in range(row_count) if row * width + column < original_length)
        for column in range(width)
    ]
    columns = [""] * width
    offset = 0
    for column in sorted(range(width), key=lambda index: key[index]):
        length = column_lengths[column]
        columns[column] = value[offset : offset + length]
        offset += length
    return "".join(
        columns[column][row]
        for row in range(row_count)
        for column in range(width)
        if row * width + column < original_length
    )


def _decode_bacon(value: str) -> str:
    decoded = []
    for token in value.split(" "):
        if len(token) == 5 and all(char in "AaBb" for char in token):
            number = int("".join("1" if char.lower() == "b" else "0" for char in token), 2)
            char = chr(ord("a") + number)
            decoded.append(char.upper() if token[0].isupper() else char)
        else:
            decoded.append(token)
    return "".join(decoded)


def _checker_bytes(kind: str, source: str) -> bytes:
    if kind == "python":
        tree = ast.parse(source)
        values = next(
            ast.literal_eval(node.value)
            for node in tree.body
            if isinstance(node, ast.Assign)
            and any(isinstance(target, ast.Name) and target.id == "expected" for target in node.targets)
        )
        return bytes(value ^ ((index * 7 + 29) & 0xFF) for index, value in enumerate(values))

    match = re.search(r"const expected = (\[[^\]]*\]);", source)
    if match is None:
        raise AssertionError("JavaScript checker expected array was not found")
    values = ast.literal_eval(match.group(1))
    return bytes((value - index * 3) & 0xFF for index, value in enumerate(values))


def _decode(kind: str, source: str, original_length: int) -> str:
    text = source.rstrip("\n")
    if kind == "base64":
        raw = base64.b64decode(text)
    elif kind == "base32":
        raw = base64.b32decode(text)
    elif kind == "hex":
        raw = bytes.fromhex(text)
    elif kind == "rot13":
        text = codecs.decode(text, "rot_13")
        raw = text.encode("ascii")
    elif kind == "caesar":
        return _unshift(text, -7)
    elif kind == "xor":
        raw = bytes(byte ^ 0x37 for byte in bytes.fromhex(text))
    elif kind == "binary":
        raw = bytes(int(group, 2) for group in text.split())
    elif kind == "double":
        raw = base64.b64decode(bytes.fromhex(text))
    elif kind in {"python", "javascript"}:
        raw = _checker_bytes(kind, source)
    elif kind == "base85":
        raw = base64.b85decode(text)
    elif kind == "url_percent":
        raw = bytes.fromhex(text.replace("%", ""))
    elif kind == "decimal_bytes":
        raw = bytes(int(value) for value in text.split())
    elif kind == "octal_bytes":
        raw = bytes(int(value, 8) for value in text.split())
    elif kind == "atbash":
        decoded = []
        for char in text:
            if "a" <= char <= "z":
                decoded.append(chr(ord("z") - (ord(char) - ord("a"))))
            elif "A" <= char <= "Z":
                decoded.append(chr(ord("Z") - (ord(char) - ord("A"))))
            else:
                decoded.append(char)
        return "".join(decoded)
    elif kind == "rot47":
        return _undo_rot47(text)
    elif kind == "vigenere_lemon":
        return _decrypt_vigenere(text, "LEMON")
    elif kind == "vigenere_key":
        return _decrypt_vigenere(text, "KEY")
    elif kind == "affine":
        return _decrypt_affine(text)
    elif kind == "xor_ice":
        raw = _xor_repeating(bytes.fromhex(text), b"ICE")
    elif kind == "xor_lock":
        raw = _xor_repeating(bytes.fromhex(text), b"LOCK")
    elif kind == "bitwise_not":
        raw = bytes(byte ^ 0xFF for byte in bytes.fromhex(text))
    elif kind == "nibble_swap":
        raw = bytes((((byte << 4) & 0xF0) | (byte >> 4)) for byte in bytes.fromhex(text))
    elif kind == "reverse_text":
        return text[::-1]
    elif kind == "rail_fence":
        return _unrail_fence(text, 3)
    elif kind == "columnar":
        return _uncolumnar(text, "ORBIT", original_length)
    elif kind == "bacon":
        return _decode_bacon(text)
    elif kind == "quoted_printable":
        raw = quopri.decodestring(text.encode("ascii"))
    elif kind == "uuencode":
        raw = binascii.a2b_uu(text.encode("ascii"))
    elif kind == "base64_url":
        raw = base64.urlsafe_b64decode(text)
    elif kind == "base64_reverse":
        raw = base64.b64decode(text[::-1])
    elif kind == "base64_rot13":
        raw = base64.b64decode(codecs.decode(text, "rot_13"))
    elif kind == "hex_reverse":
        raw = bytes.fromhex(text[::-1])
    elif kind == "zlib_hex":
        raw = zlib.decompress(bytes.fromhex(text))
    elif kind == "gzip_hex":
        raw = gzip.decompress(bytes.fromhex(text))
    elif kind == "base32_reverse":
        raw = base64.b32decode(text[::-1])
    elif kind == "base85_reverse":
        raw = base64.b85decode(text[::-1])
    elif kind == "affine_rail_fence":
        raw = _decrypt_affine(_unrail_fence(text, 3)).encode("ascii")
    elif kind == "zlib_xor":
        raw = zlib.decompress(_xor_repeating(bytes.fromhex(text), b"LOCK"))
    elif kind == "vigenere_reverse":
        return _decrypt_vigenere(text[::-1], "ORANGE")
    elif kind == "bacon_base64":
        return _decode_bacon(base64.b64decode(text).decode("ascii"))
    elif kind == "zlib_base64":
        raw = zlib.decompress(base64.b64decode(text))
    else:
        raise AssertionError("no decoder for generated puzzle kind")
    return raw.decode("ascii")


class EasyTasksTest(unittest.TestCase):
    def test_original_ten_outputs_are_unchanged(self) -> None:
        specs = easy_tasks.generated_specs(bytes(32), 180)[:10]
        self.assertEqual(
            [spec["slug"] for spec in specs],
            [
                "task13-base64",
                "task14-base32",
                "task15-hex",
                "task16-rot13",
                "task17-caesar",
                "task18-xor",
                "task19-binary",
                "task20-python-checker",
                "task21-js-checker",
                "task22-double",
            ],
        )
        self.assertEqual(_catalog_fingerprint(specs), ORIGINAL_TEN_FINGERPRINT)

    def test_manifest_has_expected_final_category_counts(self) -> None:
        manifest = _manifest_from_source()
        static_counts = Counter((kind, category) for category, _, _, _, kind in manifest)
        generated_specs = easy_tasks.generated_specs(bytes(range(32)), 180)
        generated_counts = Counter(
            (spec["kind"], spec["category"]) for spec in generated_specs
        )
        total_counts = static_counts + generated_counts
        self.assertEqual(sum(total_counts.values()), 54)
        self.assertEqual(
            total_counts,
            Counter(
                {
                    ("normal", "crypto"): 35,
                    ("normal", "web"): 4,
                    ("normal", "reverse"): 3,
                    ("normal", "forensics"): 1,
                    ("normal", "pwn"): 1,
                    ("golden", "crypto"): 8,
                    ("golden", "web"): 1,
                    ("golden", "reverse"): 1,
                }
            ),
        )

    def test_catalog_is_deterministic_and_seeded_per_slug(self) -> None:
        seed = bytes(range(32))
        first = easy_tasks.generated_specs(seed, 180)
        second = easy_tasks.generated_specs(seed, 180)
        other_seed = easy_tasks.generated_specs(bytes(range(1, 33)), 180)
        self.assertEqual(len(first), 42)
        self.assertEqual(len({spec["slug"] for spec in first}), 42)
        self.assertEqual(len({spec["title"] for spec in first}), 42)
        self.assertEqual(len({spec["flag"] for spec in first}), 42)
        self.assertTrue(all(left == right for left, right in zip(first, second)))
        self.assertTrue(
            all(left["flag"] != right["flag"] for left, right in zip(first, other_seed))
        )

    def test_every_generated_archive_is_valid_and_contains_no_plaintext_flag(self) -> None:
        specs = easy_tasks.generated_specs(bytes(range(32)), 180)
        for spec in specs:
            with zipfile.ZipFile(io.BytesIO(spec["source_archive"])) as archive:
                self.assertIsNone(archive.testzip(), f"invalid archive for {spec['slug']}")
                names = archive.namelist()
                self.assertEqual(names, sorted(names), f"archive entries are not sorted for {spec['slug']}")
                self.assertTrue(all(".." not in Path(name).parts for name in names))
                for info in archive.infolist():
                    self.assertEqual(info.date_time, (2020, 1, 1, 0, 0, 0))
                contents = [archive.read(name) for name in names]
                flag = spec["flag"].encode("ascii")
                self.assertFalse(
                    any(flag in content for content in contents),
                    f"archive contains plaintext flag for {spec['slug']}",
                )

    def test_each_downloaded_puzzle_round_trips(self) -> None:
        specs = easy_tasks.generated_specs(bytes(range(32)), 180)
        for manifest_entry, spec in zip(easy_tasks.EASY_TASKS, specs):
            puzzle_kind = manifest_entry[3]
            with zipfile.ZipFile(io.BytesIO(spec["source_archive"])) as archive:
                challenge_files = [
                    name
                    for name in archive.namelist()
                    if name not in {"name.txt", "category.txt", "description.txt"}
                ]
                self.assertEqual(len(challenge_files), 1, f"unexpected files for {spec['slug']}")
                source = archive.read(challenge_files[0]).decode("utf-8")
            recovered = _decode(puzzle_kind, source, len(spec["flag"]))
            self.assertTrue(recovered == spec["flag"], f"round-trip failed for {spec['slug']}")


if __name__ == "__main__":
    unittest.main()
