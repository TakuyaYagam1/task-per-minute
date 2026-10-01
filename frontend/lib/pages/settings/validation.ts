const EMAIL_LOCAL_PART_PATTERN = /^[A-Za-z0-9!#$%&'*+\/=?.^_`{|}~-]+$/u;
const DNS_LABEL_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/u;
const PASSWORD_LOWERCASE_PATTERN = /\p{Ll}/u;
const PASSWORD_UPPERCASE_PATTERN = /\p{Lu}/u;
const PASSWORD_DECIMAL_PATTERN = /\p{Nd}/u;
const PASSWORD_PUNCTUATION_OR_SYMBOL_PATTERN = /[\p{P}\p{S}]/u;

const isValidUnicode = (value: string): boolean => {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const nextCodeUnit = value.charCodeAt(index + 1);
      if (!(nextCodeUnit >= 0xdc00 && nextCodeUnit <= 0xdfff)) return false;
      index += 1;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      return false;
    }
  }
  return true;
};

export const settingsEmailError = (input: string): string | null => {
  const email = input.trim();
  if (!email) return "Введите email.";
  if (new TextEncoder().encode(email).length > 254) return "Email слишком длинный.";

  const separator = email.indexOf("@");
  if (separator <= 0 || separator !== email.lastIndexOf("@")) {
    return "Введите полный email, например name@example.com.";
  }

  const localPart = email.slice(0, separator);
  const domain = email.slice(separator + 1);
  if (
    localPart.length > 64 ||
    localPart.startsWith(".") ||
    localPart.endsWith(".") ||
    localPart.includes("..") ||
    !EMAIL_LOCAL_PART_PATTERN.test(localPart)
  ) {
    return "Введите полный email, например name@example.com.";
  }

  const labels = domain.split(".");
  if (
    labels.length < 2 ||
    labels.some((label) => label.length > 63 || !DNS_LABEL_PATTERN.test(label))
  ) {
    return "Введите полный email, например name@example.com.";
  }

  const finalLabel = labels[labels.length - 1];
  const isAsciiTopLevelDomain = /^[A-Za-z]{2,63}$/u.test(finalLabel);
  const isPunycodeTopLevelDomain = /^xn--.+$/iu.test(finalLabel);
  if (!isAsciiTopLevelDomain && !isPunycodeTopLevelDomain) {
    return "Введите полный email, например name@example.com.";
  }

  return null;
};

export const settingsEmailCodeError = (code: string): string | null => {
  if (code.length === 0) return "Введите код из письма.";
  if (!/^[0-9]{6}$/u.test(code)) return "Введите 6 цифр.";
  return null;
};

export const settingsPasswordError = (password: string): string | null => {
  if (!password) return "Введите новый пароль.";
  if (!isValidUnicode(password) || password.includes("\u0000")) {
    return "Пароль содержит недопустимый символ.";
  }

  const length = Array.from(password).length;
  if (length < 10 || length > 128) {
    return "Пароль должен содержать от 10 до 128 символов.";
  }
  if (new TextEncoder().encode(password).length > 512) return "Пароль слишком длинный.";
  if (
    !PASSWORD_LOWERCASE_PATTERN.test(password) ||
    !PASSWORD_UPPERCASE_PATTERN.test(password) ||
    !PASSWORD_DECIMAL_PATTERN.test(password) ||
    !PASSWORD_PUNCTUATION_OR_SYMBOL_PATTERN.test(password)
  ) {
    return "Нужны строчная и заглавная буквы, цифра и спецсимвол.";
  }

  return null;
};
