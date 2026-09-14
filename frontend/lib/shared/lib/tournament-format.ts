/**
 * Presentation labels for tournament data.
 *
 * Server values stay in the API adapters. This module only turns already
 * validated values into text that is safe to show in the interface.
 */

export const TOURNAMENT_STATE_LABELS = {
  draft: "Черновик",
  registration: "Регистрация",
  roster_locked: "Состав зафиксирован",
  swiss: "Швейцарский этап",
  golden: "Золотой этап",
  playoffs: "Плей-офф",
  technical_pause: "Техническая пауза",
  completed: "Завершен",
  cancelled: "Отменен",
} as const satisfies Readonly<Record<string, string>>;

export const SERIES_STATE_LABELS = {
  planned: "Запланирована",
  locked: "Зафиксирована",
  draft: "Подготовка",
  ready: "Готова к старту",
  active: "Идет",
  replay_required: "Нужен повтор",
  technical_pause: "Техническая пауза",
  completed: "Завершена",
  cancelled: "Отменена",
} as const satisfies Readonly<Record<string, string>>;

export const WAVE_STATE_LABELS = {
  planned: "Запланирована",
  ready_window_open: "Окно готовности открыто",
  ready: "Все готовы",
  active: "Идет",
  paused: "Приостановлена",
  completed: "Завершена",
  ready_window_expired: "Окно готовности закрыто",
  superseded: "Заменена новой версией",
} as const satisfies Readonly<Record<string, string>>;

export const READY_WINDOW_STATE_LABELS = {
  open: "Открыто",
  consumed: "Использовано",
  expired: "Истекло",
  superseded: "Заменено новой версией",
} as const satisfies Readonly<Record<string, string>>;

export const GAME_STATE_LABELS = {
  planned: "Запланирована",
  ready: "Готова к старту",
  active: "Идет",
  paused: "Приостановлена",
  completed: "Завершена",
  void: "Аннулирована",
  cancelled: "Отменена",
  superseded: "Заменена новой версией",
} as const satisfies Readonly<Record<string, string>>;

export const GOLDEN_STATE_LABELS = {
  prepared: "Подготовлен",
  ready: "Готов к старту",
  active: "Идет",
  technical_pause: "Техническая пауза",
  completed: "Завершен",
  cancelled: "Отменен",
  superseded: "Заменен новой версией",
} as const satisfies Readonly<Record<string, string>>;

export const CONNECTION_STATE_LABELS = {
  connecting: "Подключение",
  connected: "На связи",
  recovering: "Восстановление связи",
  reconnected: "Связь восстановлена",
  live: "Сервер на связи",
  stale: "Данные устарели",
  awaiting_server: "Ожидаем сервер",
  disconnected: "Связь потеряна",
  rejected: "Соединение отклонено",
  closed: "Соединение закрыто",
  error: "Ошибка соединения",
} as const satisfies Readonly<Record<string, string>>;

export const RESULT_REASON_LABELS = {
  solved: "Решено",
  surrender: "Сдача",
  operator_forfeit: "Проигрыш по решению оператора",
  no_solve: "Не решено",
  task_failure: "Ошибка задания",
  common_platform_failure: "Общая ошибка платформы",
  disconnect: "Отключение участника",
  execution_epoch_break: "Перезапуск выполнения",
  no_show: "Неявка участника",
  series_cancelled: "Серия отменена",
  tournament_cancelled: "Турнир отменен",
  derived_revision_superseded: "Результат заменен новой версией",
  score_complete: "Победа по счету",
  operator_correction: "Исправление оператором",
} as const satisfies Readonly<Record<string, string>>;

export const ACTION_LABELS = {
  open_registration: "Открыть регистрацию",
  start_swiss: "Начать швейцарский этап",
  start_golden: "Начать золотой этап",
  start_playoffs: "Начать плей-офф",
  open_ready_window: "Открыть окно готовности",
  start: "Начать",
  pause: "Приостановить",
  resume: "Продолжить",
  complete: "Завершить",
  cancel: "Отменить",
  ban: "Запретить категорию",
  pick: "Выбрать категорию",
  ready: "Отметить готовность",
  cleared: "Снять готовность",
  submit: "Отправить флаг",
  surrender: "Сдаться",
  acknowledge_result: "Подтвердить результат",
  request_next_assignment: "Получить следующее задание",
  leave_lobby: "Выйти из лобби",
  lock: "Зафиксировать состав",
  unlock: "Разблокировать состав",
  preflight: "Проверить готовность",
  replay: "Запустить повтор",
  forfeit: "Зафиксировать проигрыш",
  update: "Изменить",
  delete: "Удалить",
  retry: "Повторить попытку",
} as const satisfies Readonly<Record<string, string>>;

export const CATEGORY_LABELS = {
  web: "Web",
  crypto: "Crypto",
  forensics: "Forensics",
  reverse: "Reverse",
  pwn: "Pwn",
  steganography: "Стеганография",
  ppc: "PPC",
  osint: "OSINT",
  mobile: "Mobile",
  hardware: "Hardware",
  misc: "Разное",
} as const satisfies Readonly<Record<string, string>>;

export const ERROR_LABELS = {
  transport: "Не удалось связаться с сервером",
  unauthorized: "Сессия истекла. Войдите снова",
  forbidden: "Недостаточно прав для этого действия",
  not_found: "Турнир или ресурс не найден",
  conflict: "Состояние изменилось. Обновите данные",
  validation: "Проверьте введенные данные",
  rate_limited: "Слишком много запросов. Повторите позже",
  http: "Сервер временно недоступен",
  bad_request: "Некорректный запрос",
  request_entity_too_large: "Запрос слишком большой",
  unsupported_media_type: "Неподдерживаемый формат данных",
  unexpected_server_problem: "Сервер временно недоступен",
  tournament_revision_conflict: "Версия турнира устарела. Обновите данные",
  projection_revision_conflict: "Версия данных устарела. Обновите данные",
  golden_runtime_conflict: "Состояние золотого этапа изменилось. Обновите данные",
  invalid_request: "Проверьте запрос",
  internal_server_error: "Сервер временно недоступен",
  network_error: "Не удалось связаться с сервером",
  validation_failed: "Проверьте введенные данные",
  "validation failed": "Проверьте введенные данные",
  "not found": "Турнир или ресурс не найден",
  "rate limited": "Слишком много запросов. Повторите позже",
  "request entity too large": "Запрос слишком большой",
  "unsupported media type": "Неподдерживаемый формат данных",
  "unexpected server problem": "Сервер временно недоступен",
} as const satisfies Readonly<Record<string, string>>;

export const PREFLIGHT_CODE_LABELS = {
  "tournament.preflight.structure.roster_complete": "Состав участников заполнен",
  "tournament.preflight.structure.attendance": "Посещаемость проверена",
  "tournament.preflight.structure.participant_exclusive": "Участники не дублируются",
  "tournament.preflight.structure.preset": "Пресет турнира проверен",
  "tournament.preflight.structure.categories": "Категории проверены",
  "tournament.preflight.structure.pairings": "Пары проверены",
  "tournament.preflight.structure.byes": "Свободные места проверены",
  "tournament.preflight.structure.overrides": "Исключения проверены",
  "tournament.preflight.tasks.pool_configuration": "Пулы заданий настроены",
  "tournament.preflight.tasks.inventory": "Задания проверены",
  "tournament.preflight.tasks.missing": "Все задания на месте",
  "tournament.preflight.tasks.disabled": "Отключенные задания проверены",
  "tournament.preflight.tasks.unhealthy": "Состояние заданий проверено",
  "tournament.preflight.tasks.mutable": "Неизменяемость заданий проверена",
  "tournament.preflight.tasks.publicly_exposed": "Публичный доступ к заданиям проверен",
  "tournament.preflight.tasks.wrong_pool": "Пулы заданий проверены",
  "tournament.preflight.runtime.configuration": "Конфигурация проверена",
  "tournament.preflight.runtime.authoritative_storage": "Основное хранилище проверено",
  "tournament.preflight.runtime.submission": "Прием ответов проверен",
  "tournament.preflight.runtime.task_delivery": "Выдача заданий проверена",
  "tournament.preflight.runtime.realtime": "Обновления в реальном времени проверены",
  "tournament.preflight.runtime.capacity": "Емкость системы проверена",
  "tournament.preflight.runtime.clock": "Синхронизация времени проверена",
  "tournament.preflight.runtime.dependencies": "Зависимости проверены",
  "tournament.preflight.runtime.schedule": "Расписание проверено",
} as const satisfies Readonly<Record<string, string>>;

export const UNKNOWN_STATE_LABEL = "Состояние недоступно";
export const UNKNOWN_CONNECTION_LABEL = "Состояние соединения недоступно";
export const UNKNOWN_ERROR_LABEL = "Неизвестная ошибка сервера";
export const UNKNOWN_ACTION_LABEL = "Действие недоступно";
export const UNKNOWN_RESULT_REASON_LABEL = "Причина результата недоступна";
export const UNKNOWN_CATEGORY_LABEL = "Категория недоступна";
export const UNKNOWN_FORMAT_LABEL = "Формат недоступен";
export const UNKNOWN_DATE_TIME_LABEL = "Дата недоступна";
export const UNKNOWN_DURATION_LABEL = "Время недоступно";
export const UNKNOWN_POINTS_LABEL = "Очки недоступны";

const normalizeCode = (value: unknown): string | null => {
  if (typeof value !== "string") {
    return null;
  }

  const normalized = value.trim().toLowerCase();
  return normalized.length > 0 ? normalized : null;
};

const lookupLabel = (
  value: unknown,
  labels: Readonly<Record<string, string>>,
  fallback: string,
): string => {
  const code = normalizeCode(value);
  if (code === null) {
    return fallback;
  }

  const direct = labels[code];
  if (direct !== undefined) {
    return direct;
  }

  const shortCode = code.split(/[\/#:]/).pop();
  if (shortCode !== undefined && labels[shortCode] !== undefined) {
    return labels[shortCode];
  }

  return fallback;
};

export const formatTournamentState = (state: unknown): string =>
  lookupLabel(state, TOURNAMENT_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatTournamentStatus = formatTournamentState;

export const formatSeriesState = (state: unknown): string =>
  lookupLabel(state, SERIES_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatWaveState = (state: unknown): string =>
  lookupLabel(state, WAVE_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatReadyWindowState = (state: unknown): string =>
  lookupLabel(state, READY_WINDOW_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatGameState = (state: unknown): string =>
  lookupLabel(state, GAME_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatGameStatus = formatGameState;

export const formatGoldenState = (state: unknown): string =>
  lookupLabel(state, GOLDEN_STATE_LABELS, UNKNOWN_STATE_LABEL);

export const formatConnectionState = (state: unknown): string =>
  lookupLabel(state, CONNECTION_STATE_LABELS, UNKNOWN_CONNECTION_LABEL);

export const formatConnectionStatus = formatConnectionState;

export const formatResultReason = (reason: unknown): string =>
  lookupLabel(reason, RESULT_REASON_LABELS, UNKNOWN_RESULT_REASON_LABEL);

export const formatAction = (action: unknown): string =>
  lookupLabel(action, ACTION_LABELS, UNKNOWN_ACTION_LABEL);

export const formatCategory = (category: unknown): string =>
  lookupLabel(category, CATEGORY_LABELS, UNKNOWN_CATEGORY_LABEL);

export const formatArenaCategory = formatCategory;

export const formatSeriesFormat = (format: unknown): string => {
  const code = normalizeCode(format);
  if (code === "bo1" || code === "bo3") {
    return code.toUpperCase();
  }
  return UNKNOWN_FORMAT_LABEL;
};

export const formatFormat = formatSeriesFormat;

const statusErrorLabels: Readonly<Record<number, string>> = {
  400: ERROR_LABELS.bad_request,
  401: ERROR_LABELS.unauthorized,
  403: ERROR_LABELS.forbidden,
  404: ERROR_LABELS.not_found,
  409: ERROR_LABELS.conflict,
  413: ERROR_LABELS.request_entity_too_large,
  415: ERROR_LABELS.unsupported_media_type,
  422: ERROR_LABELS.validation,
  429: ERROR_LABELS.rate_limited,
  500: ERROR_LABELS.internal_server_error,
  502: ERROR_LABELS.internal_server_error,
  503: ERROR_LABELS.internal_server_error,
  504: ERROR_LABELS.internal_server_error,
};

export type PresentationError = Readonly<{
  code?: unknown;
  kind?: unknown;
  status?: unknown;
  type?: unknown;
}>;

export const formatError = (error: unknown): string => {
  if (typeof error === "number") {
    return statusErrorLabels[error] ?? UNKNOWN_ERROR_LABEL;
  }

  if (typeof error === "string") {
    return lookupLabel(error, { ...ERROR_LABELS, ...PREFLIGHT_CODE_LABELS }, UNKNOWN_ERROR_LABEL);
  }

  if (error && typeof error === "object" && !Array.isArray(error)) {
    const candidate = error as PresentationError;
    const kindLabel = lookupLabel(candidate.kind, ERROR_LABELS, UNKNOWN_ERROR_LABEL);
    if (kindLabel !== UNKNOWN_ERROR_LABEL) {
      return kindLabel;
    }

    const codeLabel = lookupLabel(
      candidate.code,
      { ...ERROR_LABELS, ...PREFLIGHT_CODE_LABELS },
      UNKNOWN_ERROR_LABEL,
    );
    if (codeLabel !== UNKNOWN_ERROR_LABEL) {
      return codeLabel;
    }

    const typeLabel = lookupLabel(candidate.type, ERROR_LABELS, UNKNOWN_ERROR_LABEL);
    if (typeLabel !== UNKNOWN_ERROR_LABEL) {
      return typeLabel;
    }

    if (typeof candidate.status === "number") {
      return statusErrorLabels[candidate.status] ?? UNKNOWN_ERROR_LABEL;
    }
  }

  return UNKNOWN_ERROR_LABEL;
};

export const formatServerError = formatError;
export const formatErrorCode = formatError;
export const formatArenaError = formatError;

const integerValue = (value: unknown): number | null => {
  if (typeof value !== "number" || !Number.isFinite(value) || !Number.isSafeInteger(value)) {
    return null;
  }
  return value;
};

const nonNegativeInteger = (value: unknown): number | null => {
  const integer = integerValue(value);
  return integer !== null && integer >= 0 ? integer : null;
};

const numberFormatter = new Intl.NumberFormat("ru-RU", {
  maximumFractionDigits: 0,
  useGrouping: true,
});

const russianPluralRules = new Intl.PluralRules("ru-RU");

const russianPlural = (value: number, one: string, few: string, many: string): string => {
  const forms: Record<string, string> = {
    one,
    few,
    many,
    other: many,
  };
  return forms[russianPluralRules.select(value)] ?? many;
};

export const formatPoints = (points: unknown): string => {
  const value = nonNegativeInteger(points);
  return value === null ? UNKNOWN_POINTS_LABEL : numberFormatter.format(value).replace(/\u00a0/g, " ");
};

export const formatPointsLabel = (points: unknown): string => {
  const value = nonNegativeInteger(points);
  if (value === null) {
    return UNKNOWN_POINTS_LABEL;
  }
  return `${formatPoints(value)} ${russianPlural(value, "очко", "очка", "очков")}`;
};

export const formatParticipantCount = (participants: unknown): string => {
  const value = nonNegativeInteger(participants);
  if (value === null) {
    return "Число участников недоступно";
  }
  return `${numberFormatter.format(value).replace(/\u00a0/g, " ")} ${russianPlural(
    value,
    "участник",
    "участника",
    "участников",
  )}`;
};

export const formatParticipants = formatParticipantCount;

const twoDigits = (value: number): string => String(value).padStart(2, "0");

export const formatDuration = (seconds: unknown): string => {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0) {
    return UNKNOWN_DURATION_LABEL;
  }

  const totalSeconds = Math.floor(seconds);
  const hours = Math.floor(totalSeconds / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const remainder = totalSeconds % 60;
  if (hours > 0) {
    return `${twoDigits(hours)}:${twoDigits(minutes)}:${twoDigits(remainder)}`;
  }
  return `${twoDigits(minutes)}:${twoDigits(remainder)}`;
};

export const formatServerDuration = formatDuration;

/** Formats a server clock value supplied in milliseconds as mm:ss or hh:mm:ss. */
export const formatDurationClock = (durationMs: unknown): string => {
  if (typeof durationMs !== "number" || !Number.isFinite(durationMs) || durationMs < 0) {
    return UNKNOWN_DURATION_LABEL;
  }
  return formatDuration(durationMs / 1_000);
};

export type BoScore = Readonly<{
  first_participant_wins?: unknown;
  second_participant_wins?: unknown;
  first_wins?: unknown;
  second_wins?: unknown;
  first?: unknown;
  second?: unknown;
}>;

const scorePair = (score: BoScore | readonly [unknown, unknown]): [number, number] | null => {
  const isTuple = (value: BoScore | readonly [unknown, unknown]): value is readonly [unknown, unknown] =>
    Array.isArray(value);

  if (isTuple(score)) {
    const first = nonNegativeInteger(score[0]);
    const second = nonNegativeInteger(score[1]);
    return first !== null && second !== null ? [first, second] : null;
  }

  const first = nonNegativeInteger(
    score.first_participant_wins ?? score.first_wins ?? score.first,
  );
  const second = nonNegativeInteger(
    score.second_participant_wins ?? score.second_wins ?? score.second,
  );
  return first !== null && second !== null ? [first, second] : null;
};

export function formatBoScore(first: number, second: number): string;
export function formatBoScore(score: BoScore | readonly [unknown, unknown]): string;
export function formatBoScore(
  firstOrScore: number | BoScore | readonly [unknown, unknown],
  second?: number,
): string {
  const pair =
    typeof firstOrScore === "number"
      ? scorePair([firstOrScore, second])
      : scorePair(firstOrScore);
  return pair === null ? "Счет недоступен" : `${pair[0]}:${pair[1]}`;
}

export const formatSeriesScore = formatBoScore;
export const formatMatchScore = formatBoScore;

export const formatBestOfScore = (
  firstWins: unknown,
  secondWins: unknown,
  bestOf: unknown,
): string => {
  const first = nonNegativeInteger(firstWins);
  const second = nonNegativeInteger(secondWins);
  const format = nonNegativeInteger(bestOf);
  if (first === null || second === null || format === null || format === 0) {
    return "Счет недоступен";
  }
  return `BO${format}: ${first}:${second}`;
};

export type DateTimeValue = string | number | Date | null | undefined;
export type DateTimeOptions = Readonly<{ timeZone?: string }>;

const russianDateTimeOptions: Intl.DateTimeFormatOptions = {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
};

const toDate = (value: DateTimeValue): Date | null => {
  const date = value instanceof Date ? new Date(value.getTime()) : new Date(value ?? NaN);
  return Number.isFinite(date.getTime()) ? date : null;
};

export const formatDateTime = (
  value: DateTimeValue,
  options: DateTimeOptions = {},
): string => {
  const date = toDate(value);
  if (date === null) {
    return UNKNOWN_DATE_TIME_LABEL;
  }

  try {
    const formatter = new Intl.DateTimeFormat("ru-RU", {
      ...russianDateTimeOptions,
      ...(options.timeZone === undefined ? {} : { timeZone: options.timeZone }),
    });
    return formatter.format(date).replace(/\u00a0/g, " ");
  } catch {
    return UNKNOWN_DATE_TIME_LABEL;
  }
};

export const formatDateTimeRu = formatDateTime;

export const formatArenaDateTime = (value: DateTimeValue): string =>
  formatDateTime(value, { timeZone: "Europe/Moscow" });

export const arenaActionLabel = formatAction;
