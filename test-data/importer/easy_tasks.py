"""Build small, solvable test tasks without storing their answers in Git."""

from __future__ import annotations

import base64
import binascii
import codecs
import gzip
import hashlib
import hmac
import io
import zlib
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
    ("crypto", "task23-base85", "Кодировка Base85", "base85", "normal"),
    ("crypto", "task24-url-percent", "Процентное кодирование URL", "url_percent", "normal"),
    ("crypto", "task25-decimal-bytes", "Десятичные байты", "decimal_bytes", "normal"),
    ("crypto", "task26-octal-bytes", "Восьмеричные байты", "octal_bytes", "normal"),
    ("crypto", "task27-atbash", "Шифр Атбаш", "atbash", "normal"),
    ("crypto", "task28-rot47", "Сдвиг ROT47", "rot47", "normal"),
    ("crypto", "task29-vigenere-lemon", "Шифр Виженера LEMON", "vigenere_lemon", "normal"),
    ("crypto", "task30-vigenere-key", "Шифр Виженера KEY", "vigenere_key", "normal"),
    ("crypto", "task31-affine", "Аффинный шифр", "affine", "normal"),
    ("crypto", "task32-xor-ice", "Повторяющийся XOR ICE", "xor_ice", "normal"),
    ("crypto", "task33-xor-lock", "Повторяющийся XOR LOCK", "xor_lock", "normal"),
    ("crypto", "task34-bitwise-not", "Побитовое отрицание", "bitwise_not", "normal"),
    ("crypto", "task35-nibble-swap", "Перестановка полубайтов", "nibble_swap", "normal"),
    ("crypto", "task36-reverse-text", "Обратная строка", "reverse_text", "normal"),
    ("crypto", "task37-rail-fence", "Зигзаговая перестановка", "rail_fence", "normal"),
    ("crypto", "task38-columnar", "Столбцы ORBIT", "columnar", "normal"),
    ("crypto", "task39-bacon", "Шифр Бэкона", "bacon", "normal"),
    ("crypto", "task40-quoted-printable", "Кодировка quoted-printable", "quoted_printable", "normal"),
    ("crypto", "task41-uuencode", "Кодировка uuencode", "uuencode", "normal"),
    ("crypto", "task42-base64-url", "URL-safe Base64", "base64_url", "normal"),
    ("crypto", "task43-base64-reverse", "Перевернутый Base64", "base64_reverse", "normal"),
    ("crypto", "task44-base64-rot13", "ROT13 и Base64", "base64_rot13", "normal"),
    ("crypto", "task45-hex-reverse", "Перевернутый hex", "hex_reverse", "normal"),
    ("crypto", "task46-zlib-hex", "Сжатие zlib и hex", "zlib_hex", "normal"),
    ("crypto", "task47-gzip-hex", "Сжатие gzip и hex", "gzip_hex", "normal"),
    ("crypto", "task48-base32-reverse", "Перевернутый Base32", "base32_reverse", "normal"),
    ("crypto", "task49-base85-reverse", "Base85 в обратном порядке", "base85_reverse", "golden"),
    ("crypto", "task50-affine-rail-fence", "Аффинный шифр и зигзаг", "affine_rail_fence", "golden"),
    ("crypto", "task51-zlib-xor", "Сжатие zlib и XOR", "zlib_xor", "golden"),
    ("crypto", "task52-vigenere-reverse", "Виженер и обратная строка", "vigenere_reverse", "golden"),
    ("crypto", "task53-bacon-base64", "Бэкон и Base64", "bacon_base64", "golden"),
    ("crypto", "task54-zlib-base64", "Сжатие zlib и Base64", "zlib_base64", "golden"),
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


def _atbash(value: str) -> str:
    result = []
    for char in value:
        if "a" <= char <= "z":
            result.append(chr(ord("z") - (ord(char) - ord("a"))))
        elif "A" <= char <= "Z":
            result.append(chr(ord("Z") - (ord(char) - ord("A"))))
        else:
            result.append(char)
    return "".join(result)


def _rot47(value: str) -> str:
    return "".join(
        chr(33 + ((ord(char) - 33 + 47) % 94)) if "!" <= char <= "~" else char
        for char in value
    )


def _vigenere(value: str, key: str) -> str:
    result = []
    key_index = 0
    for char in value:
        if "a" <= char <= "z" or "A" <= char <= "Z":
            base = ord("a") if char.islower() else ord("A")
            shift = ord(key[key_index % len(key)].lower()) - ord("a")
            result.append(chr((ord(char) - base + shift) % 26 + base))
            key_index += 1
        else:
            result.append(char)
    return "".join(result)


def _affine(value: str) -> str:
    result = []
    for char in value:
        if "a" <= char <= "z":
            result.append(chr((5 * (ord(char) - ord("a")) + 8) % 26 + ord("a")))
        elif "A" <= char <= "Z":
            result.append(chr((5 * (ord(char) - ord("A")) + 8) % 26 + ord("A")))
        else:
            result.append(char)
    return "".join(result)


def _xor_repeating(data: bytes, key: bytes) -> bytes:
    return bytes(byte ^ key[index % len(key)] for index, byte in enumerate(data))


def _rail_fence(value: str, rails: int) -> str:
    fences = [[] for _ in range(rails)]
    row = 0
    direction = 1
    for char in value:
        fences[row].append(char)
        if row == 0:
            direction = 1
        elif row == rails - 1:
            direction = -1
        row += direction
    return "".join("".join(fence) for fence in fences)


def _columnar(value: str, key: str) -> str:
    width = len(key)
    rows = [value[index : index + width] for index in range(0, len(value), width)]
    order = sorted(range(width), key=lambda index: key[index])
    return "".join(row[column] for column in order for row in rows if column < len(row))


def _bacon(value: str) -> str:
    tokens = []
    for char in value:
        if "A" <= char <= "Z" or "a" <= char <= "z":
            value_number = ord(char.lower()) - ord("a")
            symbols = ("A", "B") if char.isupper() else ("a", "b")
            tokens.append("".join(symbols[(value_number >> bit) & 1] for bit in range(4, -1, -1)))
        else:
            tokens.append(char)
    return " ".join(tokens)


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
    if kind == "base85":
        return (
            "Декодируйте строку Base85.",
            "challenge.txt",
            base64.b85encode(raw).decode("ascii") + "\n",
        )
    if kind == "url_percent":
        encoded = "".join(f"%{byte:02X}" for byte in raw)
        return (
            "Замените коды вида %HH соответствующими байтами UTF-8.",
            "challenge.txt",
            encoded + "\n",
        )
    if kind == "decimal_bytes":
        encoded = " ".join(str(byte) for byte in raw)
        return "Преобразуйте десятичные значения байтов в текст.", "challenge.txt", encoded + "\n"
    if kind == "octal_bytes":
        encoded = " ".join(f"{byte:03o}" for byte in raw)
        return "Преобразуйте восьмеричные байты в текст.", "challenge.txt", encoded + "\n"
    if kind == "atbash":
        return "Примените Атбаш к буквам строки.", "challenge.txt", _atbash(answer) + "\n"
    if kind == "rot47":
        return "Примените ROT47 к печатным символам.", "challenge.txt", _rot47(answer) + "\n"
    if kind == "vigenere_lemon":
        return (
            "Расшифруйте Виженера с ключом LEMON.",
            "challenge.txt",
            _vigenere(answer, "LEMON") + "\n",
        )
    if kind == "vigenere_key":
        return (
            "Расшифруйте Виженера с ключом KEY.",
            "challenge.txt",
            _vigenere(answer, "KEY") + "\n",
        )
    if kind == "affine":
        description = (
            "Шифрование букв A-Z задано формулой c=(5p+8) mod 26. "
            "Сохраните регистр, знаки не меняются."
        )
        return description, "challenge.txt", _affine(answer) + "\n"
    if kind == "xor_ice":
        encoded = _xor_repeating(raw, b"ICE").hex()
        description = "Преобразуйте hex в байты и примените повторяющийся XOR с ASCII-ключом ICE."
        return description, "challenge.txt", encoded + "\n"
    if kind == "xor_lock":
        encoded = _xor_repeating(raw, b"LOCK").hex()
        description = "Преобразуйте hex в байты и примените повторяющийся XOR с ASCII-ключом LOCK."
        return description, "challenge.txt", encoded + "\n"
    if kind == "bitwise_not":
        encoded = bytes(byte ^ 0xFF for byte in raw).hex()
        description = "Преобразуйте hex в байты, инвертируйте каждый байт и прочитайте результат как ASCII."
        return description, "challenge.txt", encoded + "\n"
    if kind == "nibble_swap":
        encoded = bytes((((byte << 4) & 0xF0) | (byte >> 4)) for byte in raw).hex()
        description = "Преобразуйте hex в байты, поменяйте полубайты местами и прочитайте ASCII."
        return description, "challenge.txt", encoded + "\n"
    if kind == "reverse_text":
        return "Прочитайте строку справа налево.", "challenge.txt", answer[::-1] + "\n"
    if kind == "rail_fence":
        return (
            "Восстановите текст, записанный зигзагом в три ряда.",
            "challenge.txt",
            _rail_fence(answer, 3) + "\n",
        )
    if kind == "columnar":
        description = (
            "Разделите строку на 5 групп по 5 символов в алфавитном порядке ключа: "
            "B,I,O,R,T. Верните столбцы в исходный порядок O,R,B,I,T, затем прочитайте строки."
        )
        return description, "challenge.txt", _columnar(answer, "ORBIT") + "\n"
    if kind == "bacon":
        description = (
            "Группы a/b и A/B содержат пять бит, a/A=0, b/B=1. "
            "Регистр группы задает регистр буквы. Цифры и знаки скопированы."
        )
        return (
            description,
            "challenge.txt",
            _bacon(answer) + "\n",
        )
    if kind == "quoted_printable":
        encoded = "".join(f"={byte:02X}" for byte in raw)
        return "Декодируйте байты в формате quoted-printable.", "challenge.txt", encoded + "\n"
    if kind == "uuencode":
        encoded = binascii.b2a_uu(raw).decode("ascii")
        return "Декодируйте одну строку в формате uuencode.", "challenge.txt", encoded
    if kind == "base64_url":
        encoded = base64.urlsafe_b64encode(raw).decode("ascii")
        return "Декодируйте URL-safe вариант Base64.", "challenge.txt", encoded + "\n"
    if kind == "base64_reverse":
        encoded = base64.b64encode(raw).decode("ascii")[::-1]
        return "Разверните строку, затем декодируйте Base64.", "challenge.txt", encoded + "\n"
    if kind == "base64_rot13":
        encoded = codecs.encode(base64.b64encode(raw).decode("ascii"), "rot_13")
        return "Сначала отмените ROT13, затем декодируйте Base64.", "challenge.txt", encoded + "\n"
    if kind == "hex_reverse":
        return "Разверните hex-строку и преобразуйте байты в текст.", "challenge.txt", raw.hex()[::-1] + "\n"
    if kind == "zlib_hex":
        return "Преобразуйте hex в байты и распакуйте zlib.", "challenge.txt", zlib.compress(raw).hex() + "\n"
    if kind == "gzip_hex":
        return (
            "Преобразуйте hex в байты и распакуйте gzip.",
            "challenge.txt",
            gzip.compress(raw, mtime=0).hex() + "\n",
        )
    if kind == "base32_reverse":
        encoded = base64.b32encode(raw).decode("ascii")[::-1]
        return "Разверните строку, затем декодируйте Base32.", "challenge.txt", encoded + "\n"
    if kind == "base85_reverse":
        encoded = base64.b85encode(raw).decode("ascii")[::-1]
        return "Разверните строку, затем декодируйте Base85.", "challenge.txt", encoded + "\n"
    if kind == "affine_rail_fence":
        encoded = _rail_fence(_affine(answer), 3)
        description = (
            "Сначала восстановите зигзаговую запись в три ряда, затем отмените шифр "
            "c=(5p+8) mod 26 для букв A-Z."
        )
        return description, "challenge.txt", encoded + "\n"
    if kind == "zlib_xor":
        compressed = zlib.compress(raw)
        encoded = _xor_repeating(compressed, b"LOCK").hex()
        description = (
            "Преобразуйте hex в байты, примените повторяющийся XOR с ASCII-ключом LOCK, "
            "затем распакуйте zlib."
        )
        return description, "challenge.txt", encoded + "\n"
    if kind == "vigenere_reverse":
        encoded = _vigenere(answer, "ORANGE")[::-1]
        return (
            "Разверните строку, затем расшифруйте Виженера с ключом ORANGE.",
            "challenge.txt",
            encoded + "\n",
        )
    if kind == "bacon_base64":
        encoded = base64.b64encode(_bacon(answer).encode("ascii")).decode("ascii")
        description = (
            "Декодируйте Base64, затем группы a/b и A/B по пять бит: a/A=0, b/B=1. "
            "Регистр группы задает регистр буквы, цифры и знаки скопированы."
        )
        return description, "challenge.txt", encoded + "\n"
    if kind == "zlib_base64":
        encoded = base64.b64encode(zlib.compress(raw)).decode("ascii")
        return "Сначала декодируйте Base64, затем распакуйте zlib.", "challenge.txt", encoded + "\n"
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
