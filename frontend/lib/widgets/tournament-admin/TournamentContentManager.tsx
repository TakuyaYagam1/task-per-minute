"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
} from "react";

import {
  ApiError,
  adminApi,
  type AdminTask,
  type CreateTaskRequest,
  type TournamentContentSelection,
  type UpdateTaskRequest,
} from "../../shared/api";
import { log, useTimedNotification } from "../../shared/lib";
import {
  Button,
  Message,
  Panel,
  Status,
  ViewportPortal,
} from "../../shared/ui";

import styles from "./TournamentContentManager.module.css";

type Task = AdminTask;
type TaskCategory = Task["category"];
type TaskDifficulty = Task["difficulty"];
type TaskKind = Task["kind"];
type LoadState = "loading" | "ready" | "error";

export type AdminRequestRunner = <T>(request: () => Promise<T>) => Promise<T>;

type TaskFormErrorField =
  | "title"
  | "description"
  | "timeLimit"
  | "flag"
  | "taskUrl"
  | "sourceFile"
  | "form";

type TaskFormErrors = Partial<Record<TaskFormErrorField, string>>;

type Notification = {
  type: "success" | "error" | "warning";
  message: string;
};

type LastUploadedSource = {
  taskTitle: string;
  fileName: string;
  url: string;
  expiresInSeconds: number;
};

type TournamentContentManagerProps = Readonly<{
  content: TournamentContentSelection | null;
  contentState: LoadState;
  contentError: string | null;
  onReloadContent: () => void;
  onSessionExpired?: () => void;
  runAdminRequest?: AdminRequestRunner;
}>;

const CATEGORY_CONFIG: Record<TaskCategory, { label: string }> = {
  web: { label: "Web" },
  crypto: { label: "Crypto" },
  forensics: { label: "Forensics" },
  reverse: { label: "Reverse" },
  pwn: { label: "Pwn" },
  steganography: { label: "Steganography" },
  ppc: { label: "PPC" },
  osint: { label: "OSINT" },
  mobile: { label: "Mobile" },
  hardware: { label: "Hardware" },
  misc: { label: "Misc" },
};

const DIFFICULTY_CONFIG: Record<TaskDifficulty, { label: string }> = {
  easy: { label: "Лёгкая" },
  medium: { label: "Средняя" },
  hard: { label: "Сложная" },
};

const KIND_CONFIG: Record<TaskKind, { label: string }> = {
  normal: { label: "Обычная" },
  golden: { label: "Золотая" },
};

const MAX_INT32 = 2_147_483_647;
const MAX_TASK_TITLE_LENGTH = 255;
const MAX_TASK_FLAG_LENGTH = 255;
const UPLOAD_SIZE_LIMIT = 100 * 1024 * 1024;

const emptyHintInputs = (): string[] => ["", "", ""];

const hintInputsFromTask = (task: Task): string[] =>
  emptyHintInputs().map((_, index) => task.hints[index] ?? "");

const hintInputsToRequest = (
  values: string[],
): NonNullable<CreateTaskRequest["hints"]> =>
  values.slice(0, 3).map((hint) => {
    const trimmed = hint.trim();
    return trimmed ? trimmed : null;
  });

const countChars = (value: string): number => Array.from(value).length;

const parsePositiveInt32 = (value: string): number | null => {
  const trimmed = value.trim();
  if (!/^[1-9]\d*$/.test(trimmed)) {
    return null;
  }
  const parsed = Number(trimmed);
  return Number.isSafeInteger(parsed) && parsed <= MAX_INT32 ? parsed : null;
};

const parsePortNumber = (value: string): number | null => {
  if (!/^\d+$/.test(value)) {
    return null;
  }
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 && parsed <= 65_535
    ? parsed
    : null;
};

const isValidHttpTaskUrl = (value: string): boolean => {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      Boolean(url.host)
    );
  } catch {
    return false;
  }
};

const isValidHostPortTaskUrl = (value: string): boolean => {
  if (value.includes("://")) {
    return false;
  }
  const separator = value.lastIndexOf(":");
  if (separator <= 0 || separator === value.length - 1) {
    return false;
  }
  const host = value.slice(0, separator).trim();
  const port = parsePortNumber(value.slice(separator + 1));
  return host.length > 0 && port !== null;
};

const isValidTaskUrl = (value: string): boolean =>
  isValidHostPortTaskUrl(value) || isValidHttpTaskUrl(value);

const isAbortError = (error: unknown): boolean =>
  error instanceof DOMException &&
  (error.name === "AbortError" || error.name === "TimeoutError");

const formatRetryAfter = (value: string | null | undefined): string => {
  if (!value) {
    return "несколько минут";
  }
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    return seconds < 60
      ? `${Math.ceil(seconds)} сек`
      : `${Math.ceil(seconds / 60)} мин`;
  }
  const retryAt = Date.parse(value);
  if (!Number.isNaN(retryAt)) {
    const secondsLeft = Math.max(1, Math.ceil((retryAt - Date.now()) / 1000));
    return secondsLeft < 60
      ? `${secondsLeft} сек`
      : `${Math.ceil(secondsLeft / 60)} мин`;
  }
  return "несколько минут";
};

const apiErrorMessage = (error: unknown, fallback: string): string => {
  if (!(error instanceof ApiError)) {
    return fallback;
  }
  if (error.status === 403) {
    return error.problem?.detail || "Нет доступа к этой операции";
  }
  if (error.status === 422) {
    return error.problem?.detail || "Некорректные данные";
  }
  if (error.status === 429) {
    return `Слишком много запросов, попробуйте через ${formatRetryAfter(error.retryAfter)}`;
  }
  return error.problem?.detail || error.message || fallback;
};

const formatRevisionID = (value: string): string => value;

export const TournamentContentManager = ({
  content,
  contentState,
  contentError,
  onReloadContent,
  onSessionExpired,
  runAdminRequest,
}: TournamentContentManagerProps) => {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [category, setCategory] = useState<TaskCategory>("web");
  const [difficulty, setDifficulty] = useState<TaskDifficulty>("easy");
  const [kind, setKind] = useState<TaskKind>("normal");
  const [timeLimit, setTimeLimit] = useState("60");
  const [flag, setFlag] = useState("");
  const [hints, setHints] = useState<string[]>(emptyHintInputs);
  const [taskUrl, setTaskUrl] = useState("");
  const [sourceFile, setSourceFile] = useState<File | null>(null);
  const [existingSourceFileURL, setExistingSourceFileURL] = useState<
    string | null
  >(null);
  const [sourceFileCleared, setSourceFileCleared] = useState(false);
  const [lastUploadedSource, setLastUploadedSource] =
    useState<LastUploadedSource | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [taskFormErrors, setTaskFormErrors] = useState<TaskFormErrors>({});
  const [editingTaskId, setEditingTaskId] = useState<string | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [tasksState, setTasksState] = useState<LoadState>("loading");
  const [tasksError, setTasksError] = useState<string | null>(null);
  const tasksControllerRef = useRef<AbortController | null>(null);
  const tasksRequestIDRef = useRef(0);
  const operationControllerRef = useRef<AbortController | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const mountedRef = useRef(false);
  const { notification, showNotification } =
    useTimedNotification<Notification>();

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      tasksControllerRef.current?.abort();
      operationControllerRef.current?.abort();
    };
  }, []);

  const notify = useCallback(
    (type: Notification["type"], message: string) => {
      showNotification({ type, message }, 4000);
    },
    [showNotification],
  );

  const request = useCallback(
    async <T,>(operation: () => Promise<T>): Promise<T> => {
      try {
        return await (runAdminRequest ? runAdminRequest(operation) : operation());
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          onSessionExpired?.();
        }
        throw error;
      }
    },
    [onSessionExpired, runAdminRequest],
  );

  const loadTasks = useCallback(
    async (options: { silent?: boolean } = {}): Promise<void> => {
      tasksControllerRef.current?.abort();
      const controller = new AbortController();
      tasksControllerRef.current = controller;
      const requestID = tasksRequestIDRef.current + 1;
      tasksRequestIDRef.current = requestID;
      const canApply = (): boolean =>
        mountedRef.current &&
        tasksRequestIDRef.current === requestID &&
        !controller.signal.aborted;

      if (!options.silent) {
        setTasksState("loading");
        setTasksError(null);
      }

      try {
        const data = await request(() => adminApi.listTasks(controller.signal));
        if (canApply()) {
          setTasks(data);
          setTasksState("ready");
          setTasksError(null);
        }
      } catch (error) {
        if (!canApply() || isAbortError(error)) {
          return;
        }
        if (options.silent) {
          log.warn("admin tasks refresh failed", error);
          return;
        }
        setTasksState("error");
        setTasksError(apiErrorMessage(error, "Не удалось загрузить задачи"));
      } finally {
        if (tasksControllerRef.current === controller) {
          tasksControllerRef.current = null;
        }
      }
    },
    [request],
  );

  useEffect(() => {
    void loadTasks();
    return () => {
      tasksControllerRef.current?.abort();
    };
  }, [loadTasks]);

  const clearTaskFormError = useCallback((field: TaskFormErrorField) => {
    setTaskFormErrors((current) => {
      if (!current[field]) {
        return current;
      }
      const next = { ...current };
      delete next[field];
      return next;
    });
  }, []);

  const resetForm = useCallback(() => {
    setEditingTaskId(null);
    setTitle("");
    setDescription("");
    setCategory("web");
    setDifficulty("easy");
    setKind("normal");
    setTimeLimit("60");
    setFlag("");
    setHints(emptyHintInputs());
    setTaskUrl("");
    setSourceFile(null);
    setExistingSourceFileURL(null);
    setSourceFileCleared(false);
    setTaskFormErrors({});
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, []);

  const startEditing = useCallback((task: Task) => {
    setEditingTaskId(task.id);
    setTitle(task.title);
    setDescription(task.description);
    setCategory(task.category);
    setDifficulty(task.difficulty);
    setKind(task.kind);
    setTimeLimit(String(task.time_limit));
    setFlag(task.flag);
    setHints(hintInputsFromTask(task));
    setTaskUrl(task.task_url ?? "");
    setSourceFile(null);
    setExistingSourceFileURL(task.source_file_url ?? null);
    setSourceFileCleared(false);
    setTaskFormErrors({});
    setLastUploadedSource(null);
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
    formRef.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, []);

  const updateHint = useCallback(
    (index: number, value: string) => {
      setHints((current) => {
        const next = [...current];
        next[index] = value;
        return next;
      });
    },
    [],
  );

  const handleFileChange = useCallback(
    (event: ChangeEvent<HTMLInputElement>) => {
      const file = event.target.files?.[0] ?? null;
      if (file && !file.name.toLowerCase().endsWith(".zip")) {
        setSourceFile(null);
        event.target.value = "";
        setTaskFormErrors((current) => ({
          ...current,
          sourceFile: "Можно загружать только ZIP-архивы",
        }));
        return;
      }
      if (file && file.size > UPLOAD_SIZE_LIMIT) {
        setSourceFile(null);
        event.target.value = "";
        setTaskFormErrors((current) => ({
          ...current,
          sourceFile: "Файл превышает 100 MB",
        }));
        return;
      }
      setSourceFile(file);
      clearTaskFormError("sourceFile");
      clearTaskFormError("form");
      if (file) {
        setSourceFileCleared(false);
      }
    },
    [clearTaskFormError],
  );

  const removeFile = useCallback(() => {
    setSourceFile(null);
    clearTaskFormError("sourceFile");
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, [clearTaskFormError]);

  const removeExistingSourceFile = useCallback(() => {
    setSourceFile(null);
    setSourceFileCleared(true);
    clearTaskFormError("sourceFile");
    if (fileInputRef.current) {
      fileInputRef.current.value = "";
    }
  }, [clearTaskFormError]);

  const restoreExistingSourceFile = useCallback(() => {
    setSourceFileCleared(false);
    clearTaskFormError("sourceFile");
  }, [clearTaskFormError]);

  const upsertTask = useCallback((task: Task) => {
    setTasks((current) => {
      const index = current.findIndex((candidate) => candidate.id === task.id);
      if (index === -1) {
        return [task, ...current];
      }
      const next = [...current];
      next[index] = task;
      return next;
    });
  }, []);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault();
    const trimmedTitle = title.trim();
    const trimmedDescription = description.trim();
    const trimmedFlag = flag.trim();
    const taskUrlValue = taskUrl.trim() || null;
    const parsedTimeLimit = parsePositiveInt32(timeLimit);
    const nextErrors: TaskFormErrors = {};

    if (!trimmedTitle || countChars(trimmedTitle) > MAX_TASK_TITLE_LENGTH) {
      nextErrors.title = "Название должно быть от 1 до 255 символов";
    }
    if (!trimmedDescription) {
      nextErrors.description = "Описание не должно быть пустым";
    }
    if (parsedTimeLimit === null) {
      nextErrors.timeLimit =
        "Лимит времени должен быть целым числом от 1 до 2147483647";
    }
    if (!trimmedFlag || countChars(trimmedFlag) > MAX_TASK_FLAG_LENGTH) {
      nextErrors.flag = "Флаг должен быть от 1 до 255 символов";
    }
    if (taskUrlValue && !isValidTaskUrl(taskUrlValue)) {
      nextErrors.taskUrl =
        "URL задания должен быть http(s) ссылкой или host:port";
    }
    if (Object.keys(nextErrors).length > 0 || parsedTimeLimit === null) {
      setTaskFormErrors(nextErrors);
      return;
    }

    setTaskFormErrors({});
    setSubmitting(true);
    operationControllerRef.current?.abort();
    const controller = new AbortController();
    operationControllerRef.current = controller;
    const taskIdToSave = editingTaskId;
    const wasEditing = taskIdToSave !== null;

    try {
      const body: CreateTaskRequest = {
        title: trimmedTitle,
        description: trimmedDescription,
        category,
        difficulty,
        kind,
        time_limit: parsedTimeLimit,
        flag: trimmedFlag,
        hints: hintInputsToRequest(hints),
        task_url: taskUrlValue,
      };
      let savedTask: Task;
      if (taskIdToSave) {
        const updateBody: UpdateTaskRequest = { ...body };
        if (sourceFileCleared) {
          updateBody.clear_source_file = true;
        }
        savedTask = await request(() =>
          adminApi.updateTask(taskIdToSave, updateBody, controller.signal),
        );
      } else {
        savedTask = await request(() =>
          adminApi.createTask(body, controller.signal),
        );
      }

      let uploadFailed = false;
      let uploadedSource: LastUploadedSource | null = null;
      if (sourceFile) {
        try {
          const upload = await request(() =>
            adminApi.uploadSource(savedTask.id, sourceFile, {
              signal: controller.signal,
            }),
          );
          uploadedSource = {
            taskTitle: savedTask.title,
            fileName: sourceFile.name,
            url: upload.source_file_url,
            expiresInSeconds: parsedTimeLimit,
          };
          savedTask = {
            ...savedTask,
            source_file_url: upload.source_file_url,
          };
        } catch (uploadError) {
          if (isAbortError(uploadError)) {
            throw uploadError;
          }
          if (uploadError instanceof Error && uploadError.message === "Unauthorized") {
            throw uploadError;
          }
          log.error("admin uploadSource failed", uploadError);
          uploadFailed = true;
        }
      }

      if (!mountedRef.current || controller.signal.aborted) {
        return;
      }
      upsertTask(savedTask);
      if (uploadFailed) {
        const uploadWarning =
          "ZIP не загрузился. Проверьте файл и повторите сохранение задачи.";
        const taskMessage = wasEditing
          ? "Задача обновлена, но файл не загрузился"
          : "Задача создана, но файл не загрузился";
        setEditingTaskId(savedTask.id);
        setExistingSourceFileURL(savedTask.source_file_url ?? null);
        setLastUploadedSource(null);
        setTaskFormErrors({
          sourceFile: uploadWarning,
          form: `${taskMessage}. Данные формы сохранены, можно повторить загрузку.`,
        });
        notify("warning", taskMessage);
      } else {
        notify("success", wasEditing ? "Задача успешно обновлена!" : "Задача успешно создана!");
        resetForm();
        setLastUploadedSource(uploadedSource);
      }
      onReloadContent();
      void loadTasks({ silent: true });
    } catch (error) {
      if (
        !mountedRef.current ||
        isAbortError(error) ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      setTaskFormErrors({
        form: apiErrorMessage(
          error,
          wasEditing
            ? "Ошибка при обновлении задачи"
            : "Ошибка при создании задачи",
        ),
      });
    } finally {
      if (operationControllerRef.current === controller) {
        operationControllerRef.current = null;
      }
      if (mountedRef.current) {
        setSubmitting(false);
      }
    }
  };

  const handleDeleteTask = async (taskId: string): Promise<void> => {
    if (!window.confirm("Вы уверены, что хотите удалить эту задачу?")) {
      return;
    }
    operationControllerRef.current?.abort();
    const controller = new AbortController();
    operationControllerRef.current = controller;
    try {
      await request(() => adminApi.deleteTask(taskId, controller.signal));
      if (!mountedRef.current || controller.signal.aborted) {
        return;
      }
      setTasks((current) => current.filter((task) => task.id !== taskId));
      if (editingTaskId === taskId) {
        resetForm();
      }
      notify("success", "Задача удалена");
      onReloadContent();
      void loadTasks({ silent: true });
    } catch (error) {
      if (
        !mountedRef.current ||
        isAbortError(error) ||
        (error instanceof Error && error.message === "Unauthorized")
      ) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        notify("error", "Нельзя удалить: задача используется в дуэлях");
      } else {
        notify("error", apiErrorMessage(error, "Ошибка при удалении задачи"));
      }
    } finally {
      if (operationControllerRef.current === controller) {
        operationControllerRef.current = null;
      }
    }
  };

  const taskUrlPlaceholder =
    category === "pwn" ? "host:port" : "https://example.com/task";

  return (
    <section className={styles.root} aria-labelledby="tournament-content-title">
      {notification && (
        <ViewportPortal>
          <div
            className={`${styles.notification} ${
              notification.type === "success"
                ? styles.notificationSuccess
                : notification.type === "warning"
                  ? styles.notificationWarning
                  : styles.notificationError
            }`}
            role="status"
          >
            {notification.message}
          </div>
        </ViewportPortal>
      )}

      <Panel
        title="Каталог контента"
        description="Управляйте задачами и исходниками, которые доступны оператору. Публикация и состав пулов остаются серверными операциями."
        className={styles.catalogPanel}
      >
        <div className={styles.contentSummary}>
          <div className={styles.summaryHeading}>
            <div>
              <h2 id="tournament-content-title" className={styles.sectionTitle}>
                Текущая публикация
              </h2>
              <p className={styles.summaryDescription}>
                Идентификаторы получены с сервера и доступны только для чтения.
              </p>
            </div>
            {contentState === "ready" && content ? (
              <Status tone="info">Ревизия {content.content_revision}</Status>
            ) : null}
          </div>

          {contentState === "loading" && (
            <Message tone="loading" title="Загружаем публикацию">
              Проверяем актуальную ревизию контента.
            </Message>
          )}
          {contentState === "error" && (
            <Message tone="error" title="Контент недоступен">
              {contentError || "Не удалось получить доступную ревизию контента"}
              <Button
                variant="secondary"
                size="small"
                className={styles.inlineButton}
                onClick={onReloadContent}
              >
                Обновить публикацию
              </Button>
            </Message>
          )}
          {contentState === "ready" && content && (
            <dl className={styles.revisionGrid} aria-label="Текущая ревизия контента">
              <div>
                <dt>Ревизия контента</dt>
                <dd>{content.content_revision}</dd>
              </div>
              <div>
                <dt>Публикация</dt>
                <dd>
                  <code>{formatRevisionID(content.publication_id)}</code>
                </dd>
              </div>
              <div>
                <dt>Нормальный пул</dt>
                <dd>
                  <code>{formatRevisionID(content.normal_pool_revision_id)}</code>
                </dd>
              </div>
              <div>
                <dt>Золотой пул</dt>
                <dd>
                  <code>{formatRevisionID(content.golden_pool_revision_id)}</code>
                </dd>
              </div>
            </dl>
          )}
        </div>

        <div className={styles.managerLayout}>
          <Panel
            as="article"
            title={editingTaskId ? "Редактировать задачу" : "Создать задачу"}
            description="Изменения сразу сохраняются через административный API."
            className={styles.formPanel}
          >
            <form ref={formRef} onSubmit={(event) => void handleSubmit(event)} className={styles.form} noValidate>
              <div className={styles.field}>
                <label htmlFor="admin-task-title">Название задачи</label>
                <input
                  id="admin-task-title"
                  name="title"
                  type="text"
                  required
                  value={title}
                  onChange={(event) => {
                    setTitle(event.target.value);
                    clearTaskFormError("title");
                    clearTaskFormError("form");
                  }}
                  placeholder="Введите название..."
                  maxLength={MAX_TASK_TITLE_LENGTH}
                  className={taskFormErrors.title ? styles.inputError : undefined}
                  aria-invalid={Boolean(taskFormErrors.title)}
                  aria-describedby={taskFormErrors.title ? "admin-task-title-error" : undefined}
                />
                {taskFormErrors.title && (
                  <p id="admin-task-title-error" className={styles.fieldError}>
                    {taskFormErrors.title}
                  </p>
                )}
              </div>

              <div className={styles.field}>
                <label htmlFor="admin-task-description">Описание</label>
                <textarea
                  id="admin-task-description"
                  name="description"
                  required
                  value={description}
                  onChange={(event) => {
                    setDescription(event.target.value);
                    clearTaskFormError("description");
                    clearTaskFormError("form");
                  }}
                  placeholder="Опишите задачу..."
                  rows={4}
                  className={taskFormErrors.description ? styles.inputError : undefined}
                  aria-invalid={Boolean(taskFormErrors.description)}
                  aria-describedby={taskFormErrors.description ? "admin-task-description-error" : undefined}
                />
                {taskFormErrors.description && (
                  <p id="admin-task-description-error" className={styles.fieldError}>
                    {taskFormErrors.description}
                  </p>
                )}
              </div>

              <div className={styles.formRow}>
                <div className={styles.field}>
                  <label htmlFor="admin-task-category">Категория</label>
                  <select
                    id="admin-task-category"
                    name="category"
                    value={category}
                    onChange={(event) => setCategory(event.target.value as TaskCategory)}
                  >
                    {Object.entries(CATEGORY_CONFIG).map(([value, config]) => (
                      <option key={value} value={value}>
                        {config.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className={styles.field}>
                  <label htmlFor="admin-task-kind">Пул задания</label>
                  <select
                    id="admin-task-kind"
                    name="kind"
                    value={kind}
                    onChange={(event) => setKind(event.target.value as TaskKind)}
                  >
                    {Object.entries(KIND_CONFIG).map(([value, config]) => (
                      <option key={value} value={value}>
                        {config.label}
                      </option>
                    ))}
                  </select>
                </div>
                <div className={styles.field}>
                  <label htmlFor="admin-task-difficulty">Сложность</label>
                  <select
                    id="admin-task-difficulty"
                    name="difficulty"
                    value={difficulty}
                    onChange={(event) => setDifficulty(event.target.value as TaskDifficulty)}
                  >
                    {Object.entries(DIFFICULTY_CONFIG).map(([value, config]) => (
                      <option key={value} value={value}>
                        {config.label}
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              <div className={styles.formRow}>
                <div className={styles.field}>
                  <label htmlFor="admin-task-time-limit">Лимит времени (сек)</label>
                  <input
                    id="admin-task-time-limit"
                    name="time_limit"
                    type="number"
                    required
                    min="1"
                    value={timeLimit}
                    onChange={(event) => {
                      setTimeLimit(event.target.value);
                      clearTaskFormError("timeLimit");
                      clearTaskFormError("form");
                    }}
                    placeholder="60"
                    className={taskFormErrors.timeLimit ? styles.inputError : undefined}
                    aria-invalid={Boolean(taskFormErrors.timeLimit)}
                    aria-describedby={taskFormErrors.timeLimit ? "admin-task-time-limit-error" : undefined}
                  />
                  {taskFormErrors.timeLimit && (
                    <p id="admin-task-time-limit-error" className={styles.fieldError}>
                      {taskFormErrors.timeLimit}
                    </p>
                  )}
                </div>
                <div className={styles.field}>
                  <label htmlFor="admin-task-flag">Флаг</label>
                  <input
                    id="admin-task-flag"
                    name="flag"
                    type="text"
                    required
                    value={flag}
                    onChange={(event) => {
                      setFlag(event.target.value);
                      clearTaskFormError("flag");
                      clearTaskFormError("form");
                    }}
                    placeholder="flag{...}"
                    maxLength={MAX_TASK_FLAG_LENGTH}
                    className={taskFormErrors.flag ? styles.inputError : undefined}
                    aria-invalid={Boolean(taskFormErrors.flag)}
                    aria-describedby={taskFormErrors.flag ? "admin-task-flag-error" : undefined}
                  />
                  {taskFormErrors.flag && (
                    <p id="admin-task-flag-error" className={styles.fieldError}>
                      {taskFormErrors.flag}
                    </p>
                  )}
                </div>
              </div>

              <div className={styles.categoryField}>
                <label htmlFor="admin-task-url" className={styles.categoryFieldLabel}>
                  {CATEGORY_CONFIG[category].label} URL
                </label>
                <input
                  id="admin-task-url"
                  name="task_url"
                  type="text"
                  value={taskUrl}
                  onChange={(event) => {
                    setTaskUrl(event.target.value);
                    clearTaskFormError("taskUrl");
                    clearTaskFormError("form");
                  }}
                  placeholder={taskUrlPlaceholder}
                  className={taskFormErrors.taskUrl ? styles.inputError : undefined}
                  aria-invalid={Boolean(taskFormErrors.taskUrl)}
                  aria-describedby={taskFormErrors.taskUrl ? "admin-task-url-error" : undefined}
                />
                {taskFormErrors.taskUrl && (
                  <p id="admin-task-url-error" className={styles.fieldError}>
                    {taskFormErrors.taskUrl}
                  </p>
                )}
              </div>

              <div className={styles.categoryField}>
                <span className={styles.categoryFieldLabel}>ZIP-архив с исходниками</span>
                <label htmlFor="admin-task-source" className={styles.fileUploadZone}>
                  <span className={styles.fileUploadText}>
                    <strong>Нажмите для выбора</strong> или перетащите ZIP-архив
                  </span>
                </label>
                <input
                  ref={fileInputRef}
                  id="admin-task-source"
                  name="source_file"
                  type="file"
                  accept=".zip,application/zip"
                  onChange={handleFileChange}
                  className={styles.visuallyHidden}
                />

                {sourceFile && (
                  <div className={styles.fileInfo}>
                    <span className={styles.fileInfoName}>
                      <strong>{sourceFile.name}</strong>
                      <span className={styles.fileInfoMeta}>
                        {(sourceFile.size / 1024 / 1024).toFixed(1)} MB - {existingSourceFileURL && !sourceFileCleared ? "заменит текущий архив после сохранения" : "загрузится после сохранения"}
                      </span>
                    </span>
                    <button type="button" className={styles.fileInfoActionDanger} onClick={removeFile}>
                      Убрать
                    </button>
                  </div>
                )}
                {!sourceFile && existingSourceFileURL && !sourceFileCleared && (
                  <div className={styles.fileInfo}>
                    <span className={styles.fileInfoName}>
                      <strong>Текущий архив сохранён</strong>
                      <span className={styles.fileInfoMeta}>Удаление применится только после сохранения задачи</span>
                    </span>
                    {editingTaskId && (
                      <a
                        className={styles.fileInfoAction}
                        href={adminApi.sourceDownloadURL(editingTaskId)}
                        download="source.zip"
                        target="_blank"
                        rel="noreferrer"
                      >
                        Скачать текущий ZIP
                      </a>
                    )}
                    <button type="button" className={styles.fileInfoActionDanger} onClick={removeExistingSourceFile}>
                      Пометить к удалению
                    </button>
                  </div>
                )}
                {!sourceFile && existingSourceFileURL && sourceFileCleared && (
                  <div className={styles.fileInfo}>
                    <span className={styles.fileInfoName}>
                      <strong>Архив будет удалён после сохранения задачи</strong>
                      <span className={styles.fileInfoMeta}>До сохранения можно отменить это действие</span>
                    </span>
                    <button type="button" className={styles.fileInfoAction} onClick={restoreExistingSourceFile}>
                      Отменить удаление
                    </button>
                  </div>
                )}
                {taskFormErrors.sourceFile && (
                  <p className={styles.fieldError}>{taskFormErrors.sourceFile}</p>
                )}
              </div>

              <div className={styles.field}>
                <span className={styles.label}>Подсказки (до 3, необязательно)</span>
                <div className={styles.hintsGrid}>
                  {hints.map((hint, index) => (
                    <input
                      key={index}
                      type="text"
                      value={hint}
                      onChange={(event) => updateHint(index, event.target.value)}
                      placeholder={`Подсказка ${index + 1}`}
                    />
                  ))}
                </div>
              </div>

              <div className={styles.buttonGroup}>
                <Button type="submit" loading={submitting} loadingLabel={editingTaskId ? "Сохраняем" : "Создаем"}>
                  {editingTaskId ? "Сохранить задачу" : "Создать задачу"}
                </Button>
                <Button type="button" variant="secondary" onClick={resetForm} disabled={submitting}>
                  {editingTaskId ? "Отменить" : "Очистить"}
                </Button>
              </div>
              {taskFormErrors.form && (
                <p className={`${styles.fieldError} ${styles.formLevelError}`} role="alert">
                  {taskFormErrors.form}
                </p>
              )}
            </form>
          </Panel>

          <Panel
            as="article"
            title="Список задач"
            description={tasksState === "ready" ? `${tasks.length} ${tasks.length === 1 ? "задача" : "задач"}` : ""}
            className={styles.listPanel}
            footer={
              <Button
                variant="secondary"
                size="small"
                onClick={() => void loadTasks()}
                loading={tasksState === "loading"}
                loadingLabel="Обновляем"
              >
                Обновить список
              </Button>
            }
          >
            {lastUploadedSource && (
              <div className={styles.sourceDownloadNotice} role="status">
                <strong>Исходники загружены в SeaweedFS</strong>
                <span>
                  {lastUploadedSource.fileName} для задачи &quot;{lastUploadedSource.taskTitle}&quot;. Ссылка временная: {lastUploadedSource.expiresInSeconds} сек.
                </span>
                <a href={lastUploadedSource.url} download={lastUploadedSource.fileName} target="_blank" rel="noreferrer">
                  Скачать загруженный ZIP
                </a>
              </div>
            )}

            {tasksState === "loading" && (
              <Message tone="loading" title="Загрузка задач">
                Получаем актуальный каталог с сервера.
              </Message>
            )}
            {tasksState === "error" && (
              <Message tone="error" title="Не удалось загрузить задачи">
                {tasksError}
                <Button variant="secondary" size="small" className={styles.inlineButton} onClick={() => void loadTasks()}>
                  Повторить
                </Button>
              </Message>
            )}
            {tasksState === "ready" && tasks.length === 0 && (
              <Message tone="empty" title="Каталог пуст">
                Пока нет созданных задач
              </Message>
            )}
            {tasksState === "ready" && tasks.length > 0 && (
              <div className={styles.taskList} aria-label="Каталог задач">
                {tasks.map((task) => {
                  const categoryInfo = CATEGORY_CONFIG[task.category];
                  const difficultyInfo = DIFFICULTY_CONFIG[task.difficulty];
                  return (
                    <article key={task.id} className={styles.taskItem}>
                      <div className={styles.taskItemInfo}>
                        <h3 className={styles.taskItemTitle}>{task.title}</h3>
                        <div className={styles.taskItemMeta}>
                          <Status tone="info" size="small">
                            {categoryInfo?.label || task.category}
                          </Status>
                          <Status tone={task.difficulty === "hard" ? "error" : task.difficulty === "medium" ? "warning" : "success"} size="small">
                            {difficultyInfo?.label || task.difficulty}
                          </Status>
                          <span className={styles.taskMetaText}>Лимит: {task.time_limit} сек</span>
                          <span className={styles.taskMetaText}>
                            Пул: {KIND_CONFIG[task.kind].label}
                          </span>
                          <span className={styles.taskMetaText}>Версия: {task.version}</span>
                          {task.source_file_url && <span className={styles.taskMetaText}>ZIP загружен</span>}
                        </div>
                      </div>
                      <div className={styles.taskItemActions}>
                        <button type="button" className={styles.taskItemButton} onClick={() => startEditing(task)} title="Редактировать задачу" aria-label={`Редактировать задачу ${task.title}`}>
                          Изменить
                        </button>
                        <button type="button" className={`${styles.taskItemButton} ${styles.taskItemButtonDanger}`} onClick={() => void handleDeleteTask(task.id)} title="Удалить задачу" aria-label={`Удалить задачу ${task.title}`}>
                          Удалить
                        </button>
                      </div>
                    </article>
                  );
                })}
              </div>
            )}
          </Panel>
        </div>
      </Panel>
    </section>
  );
};

TournamentContentManager.displayName = "TournamentContentManager";
