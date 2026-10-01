'use client';

import { useEffect, useRef, useState } from 'react';
import Image from 'next/image';

import { leaderboardApi } from '../../shared/api';
import type { LeaderboardAvatarMetadata } from '../../shared/api';

import styles from './leaderboard.module.css';

interface LeaderboardAvatarProps {
  avatar?: LeaderboardAvatarMetadata | null;
}

interface ViewportState {
  key: string | null;
  entered: boolean;
  visible: boolean;
}

interface LoadedAvatar {
  key: string;
  url: string | null;
  failed: boolean;
}

const avatarKeyFor = (avatar: LeaderboardAvatarMetadata | null | undefined): string | null =>
  avatar ? `${avatar.player_id}:${avatar.version}:${avatar.content_type}` : null;

export default function LeaderboardAvatar({ avatar }: LeaderboardAvatarProps) {
  const containerRef = useRef<HTMLSpanElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [viewport, setViewport] = useState<ViewportState>({
    key: null,
    entered: false,
    visible: false,
  });
  const [loadedAvatar, setLoadedAvatar] = useState<LoadedAvatar | null>(null);
  const [prefersReducedMotion, setPrefersReducedMotion] = useState(true);
  const [documentVisible, setDocumentVisible] = useState(true);

  const playerId = avatar?.player_id ?? null;
  const version = avatar?.version ?? null;
  const contentType = avatar?.content_type ?? null;
  const avatarKey = avatarKeyFor(avatar);
  const avatarIsVisible = viewport.key === avatarKey && viewport.visible;
  const shouldLoad = viewport.key === avatarKey && viewport.entered;
  const mediaUrl =
    loadedAvatar && loadedAvatar.key === avatarKey && !loadedAvatar.failed
      ? loadedAvatar.url
      : null;

  useEffect(() => {
    if (!avatarKey) {
      setViewport({ key: null, entered: false, visible: false });
      return;
    }

    const target = containerRef.current;
    if (!target) {
      return;
    }

    setViewport({ key: avatarKey, entered: false, visible: false });
    if (typeof IntersectionObserver === 'undefined') {
      setViewport({ key: avatarKey, entered: true, visible: true });
      return;
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        const visible = entry.isIntersecting;
        setViewport((current) =>
          current.key === avatarKey
            ? { key: avatarKey, entered: current.entered || visible, visible }
            : current,
        );
      },
      { rootMargin: '96px' },
    );
    observer.observe(target);
    return () => observer.disconnect();
  }, [avatarKey]);

  useEffect(() => {
    if (!playerId || !version || !contentType || !avatarKey || !shouldLoad) {
      return;
    }

    const controller = new AbortController();
    let objectUrl: string | null = null;
    const metadata: LeaderboardAvatarMetadata = {
      player_id: playerId,
      version,
      content_type: contentType,
    };

    void leaderboardApi
      .getAvatar(metadata, controller.signal)
      .then((blob) => {
        if (controller.signal.aborted) {
          return;
        }
        if (!blob) {
          setLoadedAvatar({ key: avatarKey, url: null, failed: true });
          return;
        }
        objectUrl = URL.createObjectURL(blob);
        setLoadedAvatar({ key: avatarKey, url: objectUrl, failed: false });
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setLoadedAvatar({ key: avatarKey, url: null, failed: true });
        }
      });

    return () => {
      controller.abort();
      if (objectUrl) {
        URL.revokeObjectURL(objectUrl);
      }
    };
  }, [avatarKey, contentType, playerId, shouldLoad, version]);

  useEffect(() => {
    const motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
    const syncMotionPreference = () => setPrefersReducedMotion(motionQuery.matches);
    const syncDocumentVisibility = () =>
      setDocumentVisible(document.visibilityState === 'visible');

    syncMotionPreference();
    syncDocumentVisibility();
    motionQuery.addEventListener('change', syncMotionPreference);
    document.addEventListener('visibilitychange', syncDocumentVisibility);
    return () => {
      motionQuery.removeEventListener('change', syncMotionPreference);
      document.removeEventListener('visibilitychange', syncDocumentVisibility);
    };
  }, []);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || contentType !== 'video/mp4' || !mediaUrl) {
      return;
    }

    let active = true;
    if (prefersReducedMotion || !documentVisible || !avatarIsVisible) {
      video.pause();
      if (prefersReducedMotion) {
        try {
          video.currentTime = 0;
        } catch {
          // The media may not have metadata yet.
        }
      }
    } else {
      void video.play().catch(() => {
        if (active) {
          setLoadedAvatar({ key: avatarKey ?? '', url: null, failed: true });
        }
      });
    }

    return () => {
      active = false;
      video.pause();
    };
  }, [avatarIsVisible, avatarKey, contentType, documentVisible, mediaUrl, prefersReducedMotion]);

  const handleMediaError = () => {
    if (avatarKey) {
      setLoadedAvatar({ key: avatarKey, url: null, failed: true });
    }
  };

  return (
    <span className={styles.avatar} ref={containerRef} aria-hidden="true">
      {mediaUrl && contentType === 'video/mp4' ? (
        <video
          ref={videoRef}
          className={styles.avatarMedia}
          src={mediaUrl}
          autoPlay={!prefersReducedMotion && documentVisible && avatarIsVisible}
          loop={!prefersReducedMotion}
          muted
          playsInline
          preload="metadata"
          onError={handleMediaError}
        />
      ) : mediaUrl && contentType ? (
        <Image
          className={styles.avatarMedia}
          src={mediaUrl}
          alt=""
          width={34}
          height={34}
          unoptimized
          onError={handleMediaError}
        />
      ) : (
        <svg
          className={styles.avatarFallback}
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeLinecap="round"
          strokeLinejoin="round"
          strokeWidth="1.7"
          aria-hidden="true"
        >
          <circle cx="12" cy="8" r="3.5" />
          <path d="M4.5 20c.6-3.5 3.3-5.5 7.5-5.5s6.9 2 7.5 5.5" />
        </svg>
      )}
    </span>
  );
}
