export const CONFIG = {
  blockWidth: "900px",
  websocketUrl:
    typeof window !== "undefined"
      ? (window.location.protocol === "https:" ? "wss://" : "ws://") +
        window.location.host +
        "/ws"
      : "ws://localhost:8080/ws",
  apiUrl:
    typeof window !== "undefined"
      ? window.location.hostname === "localhost"
        ? "http://localhost:8080"
        : window.location.origin
      : "http://localhost:8080",
};
