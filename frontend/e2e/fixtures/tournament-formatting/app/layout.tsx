import type { Metadata } from "next";

import { ThemeToggle } from "../../../../lib/shared/ui";

import "./fixture.css";

export const metadata: Metadata = {
  title: "Форматирование турнира",
};

export default function TournamentFormattingLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru" data-theme="dark">
      <body>
        <ThemeToggle storageKey="tournament-formatting-theme" />
        {children}
      </body>
    </html>
  );
}
