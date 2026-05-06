import { expect } from '@playwright/test';

export const jsonHeaders = {
  'Content-Type': 'application/json',
};

export const nowISO = () => new Date().toISOString();

export const inSecondsISO = (seconds: number) => new Date(Date.now() + seconds * 1000).toISOString();

export const expectWebSocketURLDoesNotLeakSession = (websocketURL: string): void => {
  const params = new URL(websocketURL).searchParams;
  expect(params.has('token')).toBe(false);
  expect(params.has('player_id')).toBe(false);
  expect(params.has('session_id')).toBe(false);
};
