import type { Page } from '@playwright/test';

export type WindowOpenCall = {
  url: string;
  target?: string;
  features?: string;
};

export type WebSocketErrorProbeWindow = Window & {
  __dispatchLastWebSocketError: () => boolean;
  __dispatchWebSocketErrorAt: (index: number) => boolean;
};

export const installWebSocketErrorProbe = async (page: Page): Promise<void> => {
  await page.addInitScript(() => {
    const NativeWebSocket = window.WebSocket;
    const sockets: WebSocket[] = [];
    const ProbedWebSocket = function (
      url: string | URL,
      protocols?: string | string[],
    ): WebSocket {
      const socket =
        protocols === undefined
          ? new NativeWebSocket(url)
          : new NativeWebSocket(url, protocols);
      sockets.push(socket);
      return socket;
    };

    ProbedWebSocket.prototype = NativeWebSocket.prototype;
    Object.defineProperties(ProbedWebSocket, {
      CONNECTING: { value: NativeWebSocket.CONNECTING },
      OPEN: { value: NativeWebSocket.OPEN },
      CLOSING: { value: NativeWebSocket.CLOSING },
      CLOSED: { value: NativeWebSocket.CLOSED },
    });

    window.WebSocket = ProbedWebSocket as unknown as typeof WebSocket;
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    testWindow.__dispatchWebSocketErrorAt = (index: number) => {
      const socket = sockets[index];
      return socket ? socket.dispatchEvent(new Event('error')) : false;
    };
    testWindow.__dispatchLastWebSocketError = () => {
      const socket = sockets[sockets.length - 1];
      return socket ? socket.dispatchEvent(new Event('error')) : false;
    };
  });
};
