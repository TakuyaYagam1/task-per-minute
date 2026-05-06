"use client";

import { useEffect } from "react";

const containerStyle: React.CSSProperties = {
  minHeight: "100vh",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: "1.5rem",
  background: "linear-gradient(135deg, #3E7284 0%, #2a5a6b 100%)",
  fontFamily: "Inter, -apple-system, BlinkMacSystemFont, sans-serif",
  color: "#ffffff",
  margin: 0,
};

const cardStyle: React.CSSProperties = {
  width: "100%",
  maxWidth: "32rem",
  padding: "2.5rem 2rem",
  textAlign: "center",
  background: "rgba(255, 255, 255, 0.1)",
  border: "1px solid rgba(255, 255, 255, 0.2)",
  borderRadius: "1rem",
  boxShadow: "0 10px 15px -3px rgba(0, 0, 0, 0.1)",
};

const codeStyle: React.CSSProperties = {
  fontFamily: "ui-monospace, monospace",
  fontSize: "5rem",
  fontWeight: 800,
  lineHeight: 1,
  margin: "0 0 1rem 0",
  color: "#72d1eb",
  textShadow: "0 0 20px rgba(114, 209, 235, 0.5)",
};

const titleStyle: React.CSSProperties = {
  margin: "0 0 0.75rem 0",
  fontSize: "1.5rem",
  fontWeight: 700,
  textTransform: "uppercase",
  letterSpacing: "0.02em",
};

const descStyle: React.CSSProperties = {
  margin: "0 0 1.75rem 0",
  fontSize: "1rem",
  color: "rgba(255, 255, 255, 0.8)",
  lineHeight: 1.6,
};

const buttonStyle: React.CSSProperties = {
  padding: "0.75rem 1.5rem",
  border: "none",
  borderRadius: "0.5rem",
  background: "linear-gradient(135deg, #72d1eb, #5db3d3)",
  color: "white",
  fontSize: "1rem",
  fontWeight: 500,
  cursor: "pointer",
};

export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    if (process.env.NODE_ENV !== "production") {
      // eslint-disable-next-line no-console
      console.error("global-error caught:", error);
    }
  }, [error]);

  return (
    <html lang="ru">
      <body style={{ margin: 0, padding: 0 }}>
        <main style={containerStyle}>
          <section style={cardStyle} role="alert">
            <p style={codeStyle}>500</p>
            <h1 style={titleStyle}>Критическая ошибка</h1>
            <p style={descStyle}>
              Приложение упало целиком. Попробуйте обновить страницу.
            </p>
            <button type="button" onClick={() => reset()} style={buttonStyle}>
              Перезагрузить
            </button>
          </section>
        </main>
      </body>
    </html>
  );
}
