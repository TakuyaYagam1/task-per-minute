"use client";

import { useEffect } from "react";

const containerStyle: React.CSSProperties = {
  minHeight: "100vh",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: "1.5rem",
  background: "#080d14",
  fontFamily: "Arial, sans-serif",
  color: "#f3ede1",
  margin: 0,
};

const cardStyle: React.CSSProperties = {
  width: "100%",
  maxWidth: "32rem",
  padding: "2.5rem 2rem",
  textAlign: "center",
  background: "#101b2b",
  border: "1px solid #ac8a4b",
};

const codeStyle: React.CSSProperties = {
  fontFamily: "Georgia, serif",
  fontSize: "5rem",
  fontWeight: 400,
  lineHeight: 1,
  margin: "0 0 1rem 0",
  color: "#dbbb82",
};

const titleStyle: React.CSSProperties = {
  margin: "0 0 0.75rem 0",
  fontSize: "1.5rem",
  fontFamily: "Georgia, serif",
  fontWeight: 400,
  textTransform: "uppercase",
  letterSpacing: "0.02em",
};

const descStyle: React.CSSProperties = {
  margin: "0 0 1.75rem 0",
  fontSize: "1rem",
  color: "#b8bcc4",
  lineHeight: 1.6,
};

const buttonStyle: React.CSSProperties = {
  padding: "0.75rem 1.5rem",
  border: "none",
  background: "#dbbb82",
  color: "#080d14",
  fontSize: "1rem",
  fontWeight: 600,
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
