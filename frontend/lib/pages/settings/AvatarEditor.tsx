"use client";

import Image from "next/image";
import {
  type ChangeEvent,
  type SyntheticEvent,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";

import { playerModel } from "../../entities/player";
import styles from "./AvatarEditor.module.css";

const MAX_FILE_SIZE = 5 * 1024 * 1024;
const SUPPORTED_MIME_TYPES: Readonly<Record<string, string>> = {
  gif: "image/gif",
  jpeg: "image/jpeg",
  jpg: "image/jpeg",
  mp4: "video/mp4",
  png: "image/png",
};
const FILE_TYPE_ERROR = "Выберите JPG, PNG, GIF или видео MP4.";
const FILE_SIZE_ERROR = "Размер файла должен быть не более 5 МБ.";
const DECODE_ERROR = "Не удалось открыть изображение или видео. Выберите другой файл.";
const LOAD_ERROR = "Не удалось загрузить фото профиля. Попробуйте позже.";
const VIDEO_PLAYBACK_ERROR = "Не удалось воспроизвести видео профиля.";
const VIDEO_UNAVAILABLE_ERROR = "Обработка видео сейчас недоступна. Попробуйте позже.";

const releaseObjectUrl = (objectUrlRef: { current: string | null }) => {
  const objectUrl = objectUrlRef.current;
  objectUrlRef.current = null;
  if (objectUrl) URL.revokeObjectURL(objectUrl);
};

const validateFile = (file: File): string | null => {
  if (file.size > MAX_FILE_SIZE) return FILE_SIZE_ERROR;

  const filenameParts = file.name.split(".");
  const extension = filenameParts.length > 1
    ? filenameParts.pop()?.toLowerCase()
    : undefined;
  const expectedMimeType = extension ? SUPPORTED_MIME_TYPES[extension] : undefined;
  if (!expectedMimeType || file.type.toLowerCase() !== expectedMimeType) {
    return FILE_TYPE_ERROR;
  }

  return null;
};

const avatarErrorMessage = (kind: string): string => {
  if (kind === "too_large") return FILE_SIZE_ERROR;
  if (kind === "unsupported") return FILE_TYPE_ERROR;
  if (kind === "invalid") return DECODE_ERROR;
  if (kind === "video_unavailable") return VIDEO_UNAVAILABLE_ERROR;
  return LOAD_ERROR;
};

export function AvatarEditor({ username }: Readonly<{ username: string }>) {
  const inputId = useId();
  const hintId = useId();
  const errorId = useId();
  const videoRef = useRef<HTMLVideoElement>(null);
  const activeObjectUrlRef = useRef<string | null>(null);
  const requestControllerRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewContentType, setPreviewContentType] = useState<string | null>(null);
  const [hasAvatar, setHasAvatar] = useState(false);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [canRetryLoad, setCanRetryLoad] = useState(false);
  const [prefersReducedMotion, setPrefersReducedMotion] = useState(true);
  const [videoPlaybackError, setVideoPlaybackError] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const readSavedAvatar = useCallback(async (
    requestId: number,
    signal: AbortSignal,
    missingIsError: boolean,
  ): Promise<boolean> => {
    const result = await playerModel.getAvatar(signal);
    if (signal.aborted || requestIdRef.current !== requestId) return false;

    if (result.kind === "ok" && result.avatar) {
      try {
        const objectUrl = URL.createObjectURL(result.avatar);
        releaseObjectUrl(activeObjectUrlRef);
        activeObjectUrlRef.current = objectUrl;
        setPreviewUrl(objectUrl);
        setPreviewContentType(result.avatar.type.toLowerCase());
        setHasAvatar(true);
        setCanRetryLoad(false);
        setVideoPlaybackError(false);
        setError(null);
        return true;
      } catch {
        setError(LOAD_ERROR);
        setCanRetryLoad(true);
        return false;
      }
    }

    if (result.kind === "ok" && !missingIsError) {
      releaseObjectUrl(activeObjectUrlRef);
      setPreviewUrl(null);
      setPreviewContentType(null);
      setHasAvatar(false);
      setCanRetryLoad(false);
      setVideoPlaybackError(false);
      setError(null);
      return true;
    }

    if (result.kind !== "aborted") {
      setError(result.kind === "ok" ? LOAD_ERROR : avatarErrorMessage(result.kind));
      setCanRetryLoad(true);
    }
    return false;
  }, []);

  useEffect(() => {
    const mediaQuery = window.matchMedia("(prefers-reduced-motion: reduce)");
    const updatePreference = () => setPrefersReducedMotion(mediaQuery.matches);
    updatePreference();
    mediaQuery.addEventListener("change", updatePreference);
    return () => mediaQuery.removeEventListener("change", updatePreference);
  }, []);

  useEffect(() => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    const controller = new AbortController();
    requestControllerRef.current = controller;
    let mounted = true;

    void readSavedAvatar(requestId, controller.signal, false).catch(() => {
      if (mounted && requestIdRef.current === requestId) {
        setError(LOAD_ERROR);
        setCanRetryLoad(true);
      }
    }).finally(() => {
      if (mounted && requestIdRef.current === requestId) setLoading(false);
    });

    return () => {
      mounted = false;
      requestIdRef.current += 1;
      controller.abort();
      if (requestControllerRef.current === controller) requestControllerRef.current = null;
      releaseObjectUrl(activeObjectUrlRef);
    };
  }, [readSavedAvatar]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || previewContentType !== "video/mp4" || videoPlaybackError) return;
    let visibilitySection: HTMLElement | null = null;
    for (let ancestor: HTMLElement | null = video.parentElement; ancestor && ancestor.tagName !== "MAIN"; ancestor = ancestor.parentElement) {
      if (ancestor.tagName === "SECTION") visibilitySection = ancestor;
    }
    const updatePlayback = () => {
      if (prefersReducedMotion || document.hidden || visibilitySection?.hidden) {
        video.pause();
        return;
      }
      void video.play().catch((playError: unknown) => {
        if (playError instanceof DOMException && ["AbortError", "NotAllowedError"].includes(playError.name)) return;
        if (disposed || videoRef.current !== video) return;
        setVideoPlaybackError(true);
        setError(VIDEO_PLAYBACK_ERROR);
      });
    };
    let disposed = false;
    const sectionObserver = visibilitySection ? new MutationObserver(updatePlayback) : null;
    if (visibilitySection && sectionObserver) {
      sectionObserver.observe(visibilitySection, { attributes: true, attributeFilter: ["hidden"] });
    }
    document.addEventListener("visibilitychange", updatePlayback);
    updatePlayback();
    return () => {
      disposed = true;
      document.removeEventListener("visibilitychange", updatePlayback);
      sectionObserver?.disconnect();
      video.pause();
    };
  }, [previewContentType, previewUrl, prefersReducedMotion, videoPlaybackError]);

  const retryLoad = async () => {
    if (!canRetryLoad || loading || pending) return;
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    requestControllerRef.current?.abort();
    const controller = new AbortController();
    requestControllerRef.current = controller;
    setLoading(true);
    setError(null);
    try {
      await readSavedAvatar(requestId, controller.signal, true);
    } catch {
      if (!controller.signal.aborted && requestIdRef.current === requestId) {
        setError(LOAD_ERROR);
        setCanRetryLoad(true);
      }
    } finally {
      if (requestIdRef.current === requestId) {
        requestControllerRef.current = null;
        setLoading(false);
      }
    }
  };

  const handleFileChange = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (!file || pending) return;

    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    requestControllerRef.current?.abort();

    const validationError = validateFile(file);
    if (validationError) {
      setError(validationError);
      return;
    }
    setError(null);
    setCanRetryLoad(false);
    setPending(true);
    const controller = new AbortController();
    requestControllerRef.current = controller;

    void (async () => {
      try {
        const result = await playerModel.replaceAvatar(file, controller.signal);
        if (controller.signal.aborted || requestIdRef.current !== requestId) return;
        if (result.kind === "ok") {
          setHasAvatar(true);
          const refreshed = await readSavedAvatar(requestId, controller.signal, true);
          if (!refreshed && !controller.signal.aborted && requestIdRef.current === requestId) {
            setCanRetryLoad(true);
          }
        } else if (result.kind !== "aborted") {
          setError(avatarErrorMessage(result.kind));
        }
      } catch {
        if (!controller.signal.aborted && requestIdRef.current === requestId) {
          setError(LOAD_ERROR);
          setCanRetryLoad(true);
        }
      } finally {
        if (requestIdRef.current === requestId) {
          requestControllerRef.current = null;
          setPending(false);
        }
      }
    })();
  };

  const handleVideoError = (event: SyntheticEvent<HTMLVideoElement>) => {
    if (event.currentTarget !== videoRef.current) return;
    setVideoPlaybackError(true);
    setError(VIDEO_PLAYBACK_ERROR);
  };

  const deleteAvatar = async () => {
    if (!hasAvatar || pending) return;
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    const controller = new AbortController();
    requestControllerRef.current?.abort();
    requestControllerRef.current = controller;
    setPending(true);
    setError(null);

    const result = await playerModel.deleteAvatar(controller.signal);
    if (controller.signal.aborted || requestIdRef.current !== requestId) return;
    requestControllerRef.current = null;
    if (result.kind === "ok") {
      releaseObjectUrl(activeObjectUrlRef);
      setPreviewUrl(null);
      setPreviewContentType(null);
      setHasAvatar(false);
      setCanRetryLoad(false);
      setVideoPlaybackError(false);
      setError(null);
    } else if (result.kind !== "aborted") {
      setError(avatarErrorMessage(result.kind));
    }
    setPending(false);
  };

  const initials = Array.from(username.trim()).slice(0, 2).join("").toUpperCase() || "?";

  return (
    <section className={styles.editor} aria-labelledby={`${inputId}-title`}>
      <div className={styles.copy}>
        <h3 className={styles.title} id={`${inputId}-title`}>Фото профиля</h3>
      </div>

      <div className={styles.content}>
        <div className={styles.preview}>
          {previewUrl && previewContentType === "video/mp4" && !videoPlaybackError ? (
            <>
              <video
                aria-label="Видео аватара"
                autoPlay={!prefersReducedMotion}
                className={styles.previewVideo}
                loop={!prefersReducedMotion}
                muted
                playsInline
                ref={videoRef}
                src={previewUrl}
                onError={handleVideoError}
              />
            </>
          ) : previewUrl && previewContentType !== "video/mp4" ? (
            <Image
              className={styles.previewImage}
              src={previewUrl}
              alt="Аватар профиля"
              width={112}
              height={112}
              unoptimized
            />
          ) : (
            <span className={styles.initials} role="img" aria-label="Инициалы аватара">
              {initials}
            </span>
          )}
        </div>

        <div className={styles.controls}>
          <div className={styles.actions}>
            <label className={styles.fileButton} htmlFor={inputId}>
              <span>{pending ? "Загружаем..." : "Выбрать аватар"}</span>
              <input
                className={styles.fileInput}
                id={inputId}
                type="file"
                accept=".jpg,.jpeg,.png,.gif,.mp4,image/jpeg,image/png,image/gif,video/mp4"
                aria-describedby={error ? `${hintId} ${errorId}` : hintId}
                aria-invalid={error ? true : undefined}
                disabled={loading || pending}
                onChange={handleFileChange}
              />
            </label>
            {hasAvatar ? (
              <button
                className={styles.deleteButton}
                type="button"
                disabled={loading || pending}
                onClick={() => void deleteAvatar()}
              >
                Удалить
              </button>
            ) : null}
          </div>

          <p className={styles.hint} id={hintId}>JPG, PNG, GIF или MP4 до 5 МБ. Видео - до 10 секунд, Full HD и 60 кадров/с.</p>
          {error ? <p className={styles.error} id={errorId} role="alert">{error}</p> : null}
          {canRetryLoad ? (
            <button
              className={styles.retryButton}
              type="button"
              disabled={loading || pending}
              onClick={() => void retryLoad()}
            >
              Повторить загрузку фото
            </button>
          ) : null}
        </div>
      </div>
    </section>
  );
}
