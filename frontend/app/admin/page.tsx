'use client';
import React, { useState, useEffect, useRef, useCallback } from 'react';
import styles from './admin.module.css';
import {
  adminApi,
  adminSession,
  ApiError,
  type AdminTask,
  type AdminTokenResponse,
  type CreateTaskRequest,
  type UpdateTaskRequest,
} from '../../lib/shared/api';
import { log, useTimedNotification } from '../../lib/shared/lib';

type Task = AdminTask;
type TaskCategory = Task['category'];
type TaskDifficulty = Task['difficulty'];

interface Notification {
  type: 'success' | 'error' | 'warning';
  message: string;
}

const CATEGORY_CONFIG: Record<TaskCategory, { label: string; icon: string; color: string }> = {
  web:       { label: 'Web',       icon: '🌐', color: '#72d1eb' },
  crypto:    { label: 'Crypto',    icon: '🔐', color: '#fbbf24' },
  forensics: { label: 'Forensics', icon: '🔍', color: '#a78bfa' },
  reverse:   { label: 'Reverse',   icon: '⚙️', color: '#f472b6' },
  pwn:       { label: 'Pwn',       icon: '💥', color: '#ef4444' },
  steganography: { label: 'Steganography', icon: '🖼️', color: '#38bdf8' },
  ppc:       { label: 'PPC',       icon: '🧮', color: '#fb7185' },
  osint:     { label: 'OSINT',     icon: '🛰️', color: '#22c55e' },
  mobile:    { label: 'Mobile',    icon: '📱', color: '#60a5fa' },
  hardware:  { label: 'Hardware',  icon: '🔧', color: '#f97316' },
  misc:      { label: 'Misc',      icon: '🧩', color: '#34d399' },
};

const DIFFICULTY_CONFIG: Record<TaskDifficulty, { label: string; badgeClass: string }> = {
  easy:   { label: 'Easy',   badgeClass: styles.taskBadgeEasy },
  medium: { label: 'Medium', badgeClass: styles.taskBadgeMedium },
  hard:   { label: 'Hard',   badgeClass: styles.taskBadgeHard },
};

const MAX_INT32 = 2147483647;
const MAX_TASK_TITLE_LENGTH = 255;
const MAX_TASK_FLAG_LENGTH = 255;

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
  return Number.isSafeInteger(parsed) && parsed > 0 && parsed <= 65535
    ? parsed
    : null;
};

const isValidHttpTaskUrl = (value: string): boolean => {
  try {
    const url = new URL(value);
    return (url.protocol === 'http:' || url.protocol === 'https:') && Boolean(url.host);
  } catch {
    return false;
  }
};

const isValidHostPortTaskUrl = (value: string): boolean => {
  if (value.includes('://')) {
    return false;
  }
  const portSeparator = value.lastIndexOf(':');
  if (portSeparator <= 0 || portSeparator === value.length - 1) {
    return false;
  }
  const host = value.slice(0, portSeparator).trim();
  const port = value.slice(portSeparator + 1);
  const portNumber = parsePortNumber(port);
  return host.length > 0 && portNumber !== null && portNumber <= 65535;
};

const isValidTaskUrl = (value: string): boolean =>
  isValidHostPortTaskUrl(value) || isValidHttpTaskUrl(value);

const formatRetryAfter = (value: string | null | undefined): string => {
  if (!value) return 'несколько минут';
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds > 0) {
    if (seconds < 60) return `${Math.ceil(seconds)} сек`;
    return `${Math.ceil(seconds / 60)} мин`;
  }
  const retryAt = Date.parse(value);
  if (!Number.isNaN(retryAt)) {
    const secondsLeft = Math.max(1, Math.ceil((retryAt - Date.now()) / 1000));
    if (secondsLeft < 60) return `${secondsLeft} сек`;
    return `${Math.ceil(secondsLeft / 60)} мин`;
  }
  return 'несколько минут';
};

const apiErrorMessage = (error: unknown, fallback: string): string => {
  if (!(error instanceof ApiError)) {
    return fallback;
  }
  if (error.status === 403) {
    return error.problem?.detail || 'Нет доступа к этой операции';
  }
  if (error.status === 422) {
    return error.problem?.detail || 'Некорректные данные';
  }
  if (error.status === 429) {
    return `Слишком много запросов, попробуйте через ${formatRetryAfter(error.retryAfter)}`;
  }
  return error.problem?.detail || error.message || fallback;
};

export default function AdminPanel() {
  const [tokens, setTokens] = useState<AdminTokenResponse | null>(null);
  const [password, setPassword] = useState('');
  const [authLoading, setAuthLoading] = useState(false);
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [category, setCategory] = useState<TaskCategory>('web');
  const [difficulty, setDifficulty] = useState<TaskDifficulty>('easy');
  const [timeLimit, setTimeLimit] = useState('60');
  const [flag, setFlag] = useState('');
  const [hints, setHints] = useState<string[]>(['', '', '']);
  const [taskUrl, setTaskUrl] = useState('');
  const [sourceFile, setSourceFile] = useState<File | null>(null);
  const [existingSourceFileURL, setExistingSourceFileURL] = useState<string | null>(null);
  const [sourceFileCleared, setSourceFileCleared] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [editingTaskId, setEditingTaskId] = useState<string | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [tasksLoading, setTasksLoading] = useState(false);
  const tokensRef = useRef<AdminTokenResponse | null>(null);
  const refreshPromiseRef = useRef<Promise<AdminTokenResponse> | null>(null);
  const isMountedRef = useRef(false);
  const authSessionVersionRef = useRef(0);
  const tasksRequestIDRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const passwordInputRef = useRef<HTMLInputElement>(null);
  const { notification, showNotification: showTimedNotification } =
    useTimedNotification<Notification>();

  useEffect(() => {
    isMountedRef.current = true;
    const loadedTokens = adminSession.load();
    tokensRef.current = loadedTokens;
    setTokens(loadedTokens);
    const pendingPassword = passwordInputRef.current?.value;
    if (pendingPassword) {
      setPassword(pendingPassword);
    }

    return () => {
      isMountedRef.current = false;
      authSessionVersionRef.current += 1;
    };
  }, []);

  const showNotification = useCallback((type: 'success' | 'error' | 'warning', message: string) => {
    showTimedNotification({ type, message }, 4000);
  }, [showTimedNotification]);

  const isCurrentAuthSession = useCallback((sessionVersion: number): boolean =>
    isMountedRef.current && authSessionVersionRef.current === sessionVersion,
  []);

  const saveTokens = useCallback((nextTokens: AdminTokenResponse, sessionVersion?: number) => {
    if (sessionVersion !== undefined && !isCurrentAuthSession(sessionVersion)) {
      return;
    }
    adminSession.save(nextTokens);
    tokensRef.current = nextTokens;
    setTokens(nextTokens);
  }, [isCurrentAuthSession]);

  const clearTokens = useCallback(() => {
    authSessionVersionRef.current += 1;
    tasksRequestIDRef.current += 1;
    refreshPromiseRef.current = null;
    adminSession.clear();
    tokensRef.current = null;
    setTokens(null);
    setTasks([]);
    setTasksLoading(false);
    setSubmitting(false);
  }, []);

  const refreshTokens = useCallback(async (): Promise<AdminTokenResponse> => {
    const currentTokens = tokensRef.current;
    if (!currentTokens) {
      throw new Error('Unauthorized');
    }
    const sessionVersion = authSessionVersionRef.current;
    const refreshToken = currentTokens.refresh_token;

    if (!refreshPromiseRef.current) {
      const refreshPromise = adminApi
        .refresh(refreshToken)
        .then(nextTokens => {
          if (!isCurrentAuthSession(sessionVersion)) {
            throw new Error('Unauthorized');
          }
          saveTokens(nextTokens, sessionVersion);
          return nextTokens;
        })
        .finally(() => {
          if (refreshPromiseRef.current === refreshPromise) {
            refreshPromiseRef.current = null;
          }
        });
      refreshPromiseRef.current = refreshPromise;
    }

    return refreshPromiseRef.current;
  }, [isCurrentAuthSession, saveTokens]);

  const runAdminRequest = useCallback(async <T,>(
    request: (accessToken: string) => Promise<T>,
  ): Promise<T> => {
    const currentTokens = tokensRef.current;
    if (!currentTokens) {
      throw new Error('Unauthorized');
    }
    const sessionVersion = authSessionVersionRef.current;

    try {
      const result = await request(currentTokens.access_token);
      if (!isCurrentAuthSession(sessionVersion)) {
        throw new Error('Unauthorized');
      }
      return result;
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 401) {
        throw error;
      }
      if (!isCurrentAuthSession(sessionVersion)) {
        throw new Error('Unauthorized');
      }

      const latestTokens = tokensRef.current;
      if (latestTokens && latestTokens.access_token !== currentTokens.access_token) {
        const result = await request(latestTokens.access_token);
        if (!isCurrentAuthSession(sessionVersion)) {
          throw new Error('Unauthorized');
        }
        return result;
      }

      try {
        const refreshed = await refreshTokens();
        const result = await request(refreshed.access_token);
        if (!isCurrentAuthSession(sessionVersion)) {
          throw new Error('Unauthorized');
        }
        return result;
      } catch (refreshError) {
        log.warn('admin token refresh failed', refreshError);
        if (isCurrentAuthSession(sessionVersion)) {
          clearTokens();
          showNotification('error', 'Сессия истекла. Войдите снова.');
        }
        throw new Error('Unauthorized');
      }
    }
  }, [clearTokens, isCurrentAuthSession, refreshTokens, showNotification]);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password.trim()) return;
    setAuthLoading(true);
    const sessionVersion = authSessionVersionRef.current + 1;
    refreshPromiseRef.current = null;
    try {
      const nextTokens = await adminApi.login(password);
      if (!isMountedRef.current) {
        return;
      }
      authSessionVersionRef.current = sessionVersion;
      saveTokens(nextTokens, sessionVersion);
      setPassword('');
      showNotification('success', 'Успешный вход в админ-панель');
    } catch (error) {
      if (!isMountedRef.current) {
        return;
      }
      if (error instanceof ApiError && error.status === 429) {
        showNotification('error', `Слишком много попыток. Повторите через ${formatRetryAfter(error.retryAfter)}.`);
      } else if (error instanceof ApiError && error.status === 401) {
        showNotification('error', 'Неверный пароль');
      } else {
        showNotification('error', apiErrorMessage(error, 'Ошибка подключения к серверу'));
      }
    } finally {
      if (isMountedRef.current) {
        setAuthLoading(false);
      }
    }
  };

  const handleLogout = async () => {
    const currentTokens = tokens;
    clearTokens();
    if (!currentTokens) return;
    try {
      await adminApi.logout(currentTokens.access_token, currentTokens.refresh_token);
    } catch (logoutError) {
      // Local logout wins; access tokens are short-lived if refresh revocation fails offline.
      log.warn('admin logout request failed', logoutError);
    }
  };

  const fetchTasks = useCallback(async () => {
    if (!tokens) return;
    const sessionVersion = authSessionVersionRef.current;
    const requestID = tasksRequestIDRef.current + 1;
    tasksRequestIDRef.current = requestID;
    const canApplyTasksRequest = () =>
      isCurrentAuthSession(sessionVersion) && tasksRequestIDRef.current === requestID;
    setTasksLoading(true);
    try {
      const data = await runAdminRequest(accessToken => adminApi.listTasks(accessToken));
      if (canApplyTasksRequest()) {
        setTasks(data);
      }
    } catch (error) {
      if (!canApplyTasksRequest() || (error instanceof Error && error.message === 'Unauthorized')) {
        return;
      }
      showNotification('error', apiErrorMessage(error, 'Не удалось загрузить задачи'));
    } finally {
      if (canApplyTasksRequest()) {
        setTasksLoading(false);
      }
    }
  }, [isCurrentAuthSession, runAdminRequest, showNotification, tokens]);

  useEffect(() => {
    if (tokens) fetchTasks();
  }, [tokens, fetchTasks]);

  const resetForm = useCallback(() => {
    setEditingTaskId(null);
    setTitle('');
    setDescription('');
    setCategory('web');
    setDifficulty('easy');
    setTimeLimit('60');
    setFlag('');
    setHints(['', '', '']);
    setTaskUrl('');
    setSourceFile(null);
    setExistingSourceFileURL(null);
    setSourceFileCleared(false);
    if (fileInputRef.current) fileInputRef.current.value = '';
  }, []);

  const startEditing = (task: Task) => {
    setEditingTaskId(task.id);
    setTitle(task.title);
    setDescription(task.description);
    setCategory(task.category);
    setDifficulty(task.difficulty);
    setTimeLimit(String(task.time_limit));
    setFlag(task.flag);
    setHints(task.hints.length === 3 ? task.hints : ['', '', '']);
    setTaskUrl(task.task_url ?? '');
    setSourceFile(null);
    setExistingSourceFileURL(task.source_file_url ?? null);
    setSourceFileCleared(false);
    if (fileInputRef.current) fileInputRef.current.value = '';
    window.scrollTo({ top: 0, behavior: 'smooth' });
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmedTitle = title.trim();
    if (!trimmedTitle || countChars(trimmedTitle) > MAX_TASK_TITLE_LENGTH) {
      showNotification('error', 'Название должно быть от 1 до 255 символов');
      return;
    }
    const trimmedDescription = description.trim();
    if (!trimmedDescription) {
      showNotification('error', 'Описание не должно быть пустым');
      return;
    }
    const trimmedFlag = flag.trim();
    if (!trimmedFlag || countChars(trimmedFlag) > MAX_TASK_FLAG_LENGTH) {
      showNotification('error', 'Флаг должен быть от 1 до 255 символов');
      return;
    }
    const validHints = hints.map(h => h.trim()).filter(Boolean);
    if (validHints.length !== 3) {
      showNotification('error', 'Необходимо заполнить все 3 подсказки');
      return;
    }
    const parsedTimeLimit = parsePositiveInt32(timeLimit);
    if (parsedTimeLimit === null) {
      showNotification('error', 'Лимит времени должен быть целым числом от 1 до 2147483647');
      return;
    }
    const taskUrlValue = taskUrl.trim() || null;
    if (taskUrlValue && !isValidTaskUrl(taskUrlValue)) {
      showNotification('error', 'URL задания должен быть http(s) ссылкой или host:port');
      return;
    }

    setSubmitting(true);
    const sessionVersion = authSessionVersionRef.current;
    try {
      const body: CreateTaskRequest = {
        title: trimmedTitle,
        description: trimmedDescription,
        category,
        difficulty,
        time_limit: parsedTimeLimit,
        flag: trimmedFlag,
        hints: validHints,
        task_url: taskUrlValue,
      };

      let savedTask: AdminTask;
      if (editingTaskId) {
        const updateBody: UpdateTaskRequest = { ...body };
        if (sourceFileCleared) {
          updateBody.source_file_url = null;
        }
        savedTask = await runAdminRequest(accessToken => adminApi.updateTask(accessToken, editingTaskId, updateBody));
      } else {
        savedTask = await runAdminRequest(accessToken => adminApi.createTask(accessToken, body));
      }

      let uploadFailed = false;
      if (sourceFile) {
        try {
          await runAdminRequest(accessToken => adminApi.uploadSource(accessToken, savedTask.id, sourceFile));
        } catch (uploadError) {
          if (uploadError instanceof Error && uploadError.message === 'Unauthorized') {
            throw uploadError;
          }
          log.error('admin uploadSource failed', uploadError);
          uploadFailed = true;
        }
      }
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }

      if (uploadFailed) {
        showNotification('warning', `${editingTaskId ? 'Задача обновлена' : 'Задача создана'}, но файл не загрузился`);
      } else {
        showNotification('success', editingTaskId ? 'Задача успешно обновлена!' : 'Задача успешно создана!');
      }
      resetForm();
      fetchTasks();
    } catch (err) {
      if (!isCurrentAuthSession(sessionVersion) || (err instanceof Error && err.message === 'Unauthorized')) return;
      showNotification('error', apiErrorMessage(err, editingTaskId ? 'Ошибка при обновлении задачи' : 'Ошибка при создании задачи'));
    } finally {
      if (isCurrentAuthSession(sessionVersion)) {
        setSubmitting(false);
      }
    }
  };

  const handleDeleteTask = async (taskId: string) => {
    if (!confirm('Вы уверены, что хотите удалить эту задачу?')) return;
    const sessionVersion = authSessionVersionRef.current;
    try {
      await runAdminRequest(accessToken => adminApi.deleteTask(accessToken, taskId));
      if (!isCurrentAuthSession(sessionVersion)) {
        return;
      }
      showNotification('success', 'Задача удалена');
      fetchTasks();
    } catch (error) {
      if (!isCurrentAuthSession(sessionVersion) || (error instanceof Error && error.message === 'Unauthorized')) {
        return;
      }
      if (error instanceof ApiError && error.status === 409) {
        showNotification('error', 'Нельзя удалить: задача используется в дуэлях');
      } else {
        showNotification('error', apiErrorMessage(error, 'Ошибка при удалении задачи'));
      }
    }
  };

  const updateHint = (index: number, value: string) => {
    const newHints = [...hints];
    newHints[index] = value;
    setHints(newHints);
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0] || null;
    if (file && !file.name.toLowerCase().endsWith('.zip')) {
      setSourceFile(null);
      if (fileInputRef.current) fileInputRef.current.value = '';
      showNotification('error', 'Можно загружать только ZIP-архивы');
      return;
    }
    if (file && file.size > 100 * 1024 * 1024) {
      setSourceFile(null);
      if (fileInputRef.current) fileInputRef.current.value = '';
      showNotification('error', 'Файл превышает 100 MB');
      return;
    }
    setSourceFile(file);
    if (file) {
      setSourceFileCleared(false);
    }
  };

  const removeFile = () => {
    setSourceFile(null);
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const removeExistingSourceFile = () => {
    setSourceFile(null);
    setSourceFileCleared(true);
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const restoreExistingSourceFile = () => {
    setSourceFileCleared(false);
  };

  const renderCategoryFields = () => {
    const taskURLPlaceholder = category === 'pwn' ? 'host:port' : 'https://example.com/task';

    return (
      <>
        <div className={styles.categoryField}>
          <div className={styles.categoryFieldLabel}>
            {CATEGORY_CONFIG[category].icon} {CATEGORY_CONFIG[category].label} URL
          </div>
          <div className={styles.inputGroup}>
            <input
              type="text"
              value={taskUrl}
              onChange={e => setTaskUrl(e.target.value)}
              placeholder={taskURLPlaceholder}
            />
          </div>
        </div>

        <div className={styles.categoryField}>
          <div className={styles.categoryFieldLabel}>
            {CATEGORY_CONFIG[category].icon} ZIP-архив с исходниками
          </div>
          <div className={styles.fileUpload}>
            <div
              className={styles.fileUploadZone}
              onClick={() => fileInputRef.current?.click()}
            >
              <div className={styles.fileUploadIcon}>📁</div>
              <div className={styles.fileUploadText}>
                <strong>Нажмите для выбора</strong> или перетащите ZIP-архив
              </div>
            </div>
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip"
              onChange={handleFileChange}
              style={{ display: 'none' }}
            />
            {sourceFile && (
              <div className={styles.fileInfo}>
                <span>📦</span>
                <span className={styles.fileInfoName}>{sourceFile.name}</span>
                <span>({(sourceFile.size / 1024 / 1024).toFixed(1)} MB)</span>
                <span className={styles.fileInfoRemove} onClick={removeFile}>
                  ✕
                </span>
              </div>
            )}
            {!sourceFile && existingSourceFileURL && !sourceFileCleared && (
              <div className={styles.fileInfo}>
                <span>📦</span>
                <span className={styles.fileInfoName}>Текущий архив сохранён</span>
                <span className={styles.fileInfoRemove} onClick={removeExistingSourceFile}>
                  ✕
                </span>
              </div>
            )}
            {!sourceFile && existingSourceFileURL && sourceFileCleared && (
              <div className={styles.fileInfo}>
                <span>🗑</span>
                <span className={styles.fileInfoName}>Текущий архив будет удалён</span>
                <span className={styles.fileInfoRemove} onClick={restoreExistingSourceFile}>
                  ↺
                </span>
              </div>
            )}
          </div>
        </div>
      </>
    );
  };
  if (!tokens) {
    return (
      <main className={styles.container}>
        <div className={styles.header}>
          <div className={styles.headerTop}>
            <h1 className={styles.title}>Admin</h1>
          </div>
          <p className={styles.subtitle}>Панель управления задачами</p>
        </div>

        <div className={`${styles.card} ${styles.loginCard}`}>
          <h2 className={styles.cardTitle}>Авторизация</h2>
          <form onSubmit={handleLogin} className={styles.form}>
            <div className={styles.inputGroup}>
              <label>Пароль администратора</label>
              <input
                ref={passwordInputRef}
                type="password"
                required
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="Введите пароль..."
              />
            </div>
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary}`}
              disabled={authLoading || !password.trim()}
            >
              {authLoading ? (
                <>
                  <div className={styles.spinner} style={{ width: 18, height: 18 }}></div>
                  Вход...
                </>
              ) : (
                'Войти'
              )}
            </button>
          </form>
        </div>

        {notification && (
          <div className={`${styles.notification} ${
            notification.type === 'success' ? styles.notificationSuccess :
            notification.type === 'warning' ? styles.notificationWarning :
            styles.notificationError
          }`}>
            {notification.message}
          </div>
        )}
      </main>
    );
  }
  return (
    <main className={styles.container}>
      {notification && (
        <div className={`${styles.notification} ${
          notification.type === 'success' ? styles.notificationSuccess :
          notification.type === 'warning' ? styles.notificationWarning :
          styles.notificationError
        }`}>
          {notification.message}
        </div>
      )}
      <div className={styles.header}>
        <div className={styles.headerTop}>
          <h1 className={styles.title}>Admin</h1>
        </div>
        <p className={styles.subtitle}>Панель управления задачами</p>
        <button
          type="button"
          className={`${styles.btn} ${styles.btnSecondary}`}
          onClick={handleLogout}
        >
          Выйти
        </button>
      </div>
      <div className={styles.card}>
        <h2 className={styles.cardTitle}>{editingTaskId ? '✏️ Редактировать задачу' : '➕ Создать задачу'}</h2>
        <form onSubmit={handleSubmit} className={styles.form}>
          <div className={styles.inputGroup}>
            <label>Название задачи</label>
            <input
              type="text"
              required
              value={title}
              onChange={e => setTitle(e.target.value)}
              placeholder="Введите название..."
              maxLength={255}
            />
          </div>
          <div className={styles.inputGroup}>
            <label>Описание</label>
            <textarea
              required
              value={description}
              onChange={e => setDescription(e.target.value)}
              placeholder="Опишите задачу..."
              rows={3}
            />
          </div>
          <div className={styles.formRow}>
            <div className={styles.inputGroup}>
              <label>Категория</label>
              <select
                value={category}
                onChange={e => {
                  const nextCategory = e.target.value as TaskCategory;
                  setCategory(nextCategory);
                }}
                className={styles.select}
              >
                <option value="web">🌐 Web</option>
                <option value="crypto">🔐 Crypto</option>
                <option value="forensics">🔍 Forensics</option>
                <option value="reverse">⚙️ Reverse</option>
                <option value="pwn">💥 Pwn</option>
                <option value="steganography">🖼️ Steganography</option>
                <option value="ppc">🧮 PPC</option>
                <option value="osint">🛰️ OSINT</option>
                <option value="mobile">📱 Mobile</option>
                <option value="hardware">🔧 Hardware</option>
                <option value="misc">🧩 Misc</option>
              </select>
            </div>

            <div className={styles.inputGroup}>
              <label>Сложность</label>
              <select
                value={difficulty}
                onChange={e => setDifficulty(e.target.value as TaskDifficulty)}
                className={styles.select}
              >
                <option value="easy">🟢 Лёгкая</option>
                <option value="medium">🟡 Средняя</option>
                <option value="hard">🔴 Сложная</option>
              </select>
            </div>
          </div>
          <div className={styles.formRow}>
            <div className={styles.inputGroup}>
              <label>Лимит времени (сек)</label>
              <input
                type="number"
                required
                min="1"
                value={timeLimit}
                onChange={e => setTimeLimit(e.target.value)}
                placeholder="60"
              />
            </div>

            <div className={styles.inputGroup}>
              <label>Флаг</label>
              <input
                type="text"
                required
                value={flag}
                onChange={e => setFlag(e.target.value)}
                placeholder="ctf{...}"
              />
            </div>
          </div>
          {renderCategoryFields()}
          <div className={styles.inputGroup}>
            <label>Подсказки (3 шт)</label>
            <div className={styles.hintsGrid}>
              {hints.map((hint, i) => (
                <div key={i} className={styles.hintInput}>
                  <span className={styles.hintNumber}>#{i + 1}</span>
                  <input
                    type="text"
                    required
                    value={hint}
                    onChange={e => updateHint(i, e.target.value)}
                    placeholder={`Подсказка ${i + 1}`}
                  />
                </div>
              ))}
            </div>
          </div>
          <div className={styles.btnGroup}>
            <button
              type="submit"
              className={`${styles.btn} ${styles.btnPrimary}`}
              disabled={submitting}
            >
              {submitting ? (
                <>
                  <div className={styles.spinner} style={{ width: 18, height: 18 }}></div>
                  {editingTaskId ? 'Сохранение...' : 'Создание...'}
                </>
              ) : (
                editingTaskId ? '💾 Сохранить задачу' : '🚀 Создать задачу'
              )}
            </button>
            <button
              type="button"
              className={`${styles.btn} ${styles.btnSecondary}`}
              onClick={resetForm}
            >
              {editingTaskId ? 'Отменить' : 'Очистить'}
            </button>
          </div>
        </form>
      </div>
      <div className={styles.taskList}>
        <h2 className={styles.taskListTitle}>📋 Список задач</h2>

        {tasksLoading ? (
          <div className={styles.loading}>
            <div className={styles.spinner}></div>
            <p style={{ color: 'rgba(255,255,255,0.5)', fontSize: '0.9rem' }}>Загрузка задач...</p>
          </div>
        ) : tasks.length === 0 ? (
          <div className={styles.empty}>
            <div className={styles.emptyIcon}>📭</div>
            <p className={styles.emptyText}>Пока нет созданных задач</p>
          </div>
        ) : (
          tasks.map(task => (
            <div key={task.id} className={styles.taskItem}>
              <div className={styles.taskItemInfo}>
                <div className={styles.taskItemTitle}>{task.title}</div>
                <div className={styles.taskItemMeta}>
                  <span className={`${styles.taskBadge} ${
                    task.category === 'web' ? styles.taskBadgeWeb :
                    task.category === 'crypto' ? styles.taskBadgeCrypto :
                    styles.taskBadgeFile
                  }`}>
                    {CATEGORY_CONFIG[task.category]?.icon || '📦'} {CATEGORY_CONFIG[task.category]?.label || task.category}
                  </span>
                  <span className={`${styles.taskBadge} ${DIFFICULTY_CONFIG[task.difficulty]?.badgeClass || ''}`}>
                    {DIFFICULTY_CONFIG[task.difficulty]?.label || task.difficulty}
                  </span>
                  <span style={{ fontSize: '0.65rem', color: 'rgba(255,255,255,0.3)' }}>
                    ⏱ {task.time_limit}с
                  </span>
                </div>
              </div>
              <div className={styles.taskItemActions}>
                <button
                  className={styles.taskItemBtn}
                  onClick={() => startEditing(task)}
                  title="Редактировать задачу"
                >
                  ✏️
                </button>
                <button
                  className={`${styles.taskItemBtn} ${styles.taskItemBtnDanger}`}
                  onClick={() => handleDeleteTask(task.id)}
                  title="Удалить задачу"
                >
                  🗑️
                </button>
              </div>
            </div>
          ))
        )}
      </div>
    </main>
  );
}
