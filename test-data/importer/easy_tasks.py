"""Build small, solvable test tasks without storing their answers in Git."""

from __future__ import annotations

import base64
import codecs
import hashlib
import hmac
import io
import zipfile


EASY_TASKS = (
    ("crypto", "task13-base64", "Кодировка Base64", "base64", "normal"),
    ("crypto", "task14-base32", "Кодировка Base32", "base32", "normal"),
    ("crypto", "task15-hex", "Шестнадцатеричный текст", "hex", "normal"),
    ("crypto", "task16-rot13", "Сдвиг ROT13", "rot13", "normal"),
    ("crypto", "task17-caesar", "Сдвиг на семь", "caesar", "normal"),
    ("crypto", "task18-xor", "Байты XOR", "xor", "normal"),
    ("crypto", "task19-binary", "Двоичные байты", "binary", "normal"),
    ("reverse", "task20-python-checker", "Проверка Python", "python", "normal"),
    ("reverse", "task21-js-checker", "Проверка JavaScript", "javascript", "normal"),
    ("crypto", "task22-double", "Двойная кодировка", "double", "golden"),
)


def _source_archive(files: dict[str, str]) -> bytes:
    archive = io.BytesIO()
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
        for name, value in sorted(files.items()):
            entry = zipfile.ZipInfo(name, date_time=(2020, 1, 1, 0, 0, 0))
            entry.create_system = 3
            entry.external_attr = 0o100644 << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(entry, value.encode("utf-8"))
    return archive.getvalue()


def _caesar(value: str, shift: int) -> str:
    result = []
    for char in value:
        if "a" <= char <= "z":
            result.append(chr((ord(char) - ord("a") + shift) % 26 + ord("a")))
        elif "A" <= char <= "Z":
            result.append(chr((ord(char) - ord("A") + shift) % 26 + ord("A")))
        else:
            result.append(char)
    return "".join(result)


def _puzzle(kind: str, answer: str) -> tuple[str, str, str]:
    raw = answer.encode("ascii")
    if kind == "base64":
        return "Раскодируйте строку Base64.", "challenge.txt", base64.b64encode(raw).decode("ascii") + "\n"
    if kind == "base32":
        return "Раскодируйте строку Base32.", "challenge.txt", base64.b32encode(raw).decode("ascii") + "\n"
    if kind == "hex":
        return "Переведите шестнадцатеричные байты в текст.", "challenge.txt", raw.hex() + "\n"
    if kind == "rot13":
        return "Примените ROT13 к строке.", "challenge.txt", codecs.encode(answer, "rot_13") + "\n"
    if kind == "caesar":
        return "Сдвиньте буквы на семь позиций назад.", "challenge.txt", _caesar(answer, 7) + "\n"
    if kind == "xor":
        encoded = bytes(byte ^ 0x37 for byte in raw)
        return "Примените XOR с байтом 0x37 к каждому байту строки в hex.", "challenge.txt", encoded.hex() + "\n"
    if kind == "binary":
        encoded = " ".join(f"{byte:08b}" for byte in raw)
        return "Переведите группы из восьми бит в текст.", "challenge.txt", encoded + "\n"
    if kind == "double":
        encoded = base64.b64encode(raw).hex()
        return "Сначала декодируйте hex, затем Base64.", "challenge.txt", encoded + "\n"
    if kind == "python":
        encoded = [(byte ^ ((index * 7 + 29) & 0xFF)) for index, byte in enumerate(raw)]
        checker = (
            "# Recover the input accepted by this checker.\n"
            f"expected = {encoded!r}\n"
            "candidate = input('answer: ').encode('ascii', errors='ignore')\n"
            "valid = len(candidate) == len(expected) and all(\n"
            "    byte ^ ((index * 7 + 29) & 0xFF) == expected[index]\n"
            "    for index, byte in enumerate(candidate)\n"
            ")\n"
            "print('accepted' if valid else 'rejected')\n"
        )
        return "Восстановите строку, которую принимает программа.", "checker.py", checker
    if kind == "javascript":
        encoded = [((byte + index * 3) & 0xFF) for index, byte in enumerate(raw)]
        checker = (
            "// Recover the input accepted by this checker.\n"
            f"const expected = {encoded!r};\n"
            "const candidate = process.argv[2] ?? '';\n"
            "const bytes = Buffer.from(candidate, 'utf8');\n"
            "const valid = bytes.length === expected.length && [...bytes].every(\n"
            "  (byte, index) => ((byte + index * 3) & 0xff) === expected[index]\n"
            ");\n"
            "console.log(valid ? 'accepted' : 'rejected');\n"
        )
        return "Восстановите строку, которую принимает программа.", "checker.js", checker
    raise ValueError("unknown test task generator")


def generated_specs(seed: bytes, time_limit: int) -> list[dict]:
    if len(seed) != 32:
        raise ValueError("test task seed must contain 32 bytes")
    specs = []
    for category, slug, title, puzzle_kind, task_kind in EASY_TASKS:
        digest = hmac.new(seed, slug.encode("ascii"), hashlib.sha256).hexdigest()
        answer = f"TPM{{{digest[:20]}}}"
        description, file_name, puzzle = _puzzle(puzzle_kind, answer)
        source = _source_archive(
            {
                "name.txt": title + "\n",
                "category.txt": category + "\n",
                "description.txt": description + "\n",
                file_name: puzzle,
            }
        )
        specs.append(
            {
                "slug": slug,
                "title": title,
                "description": description,
                "category": category,
                "difficulty": "easy",
                "time_limit": time_limit,
                "flag": answer,
                "kind": task_kind,
                "enabled": True,
                "task_url": None,
                "source_archive": source,
            }
        )
    return specs
