import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Контракт API Arena",
};

export default function FixtureLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru">
      <body>{children}</body>
    </html>
  );
}
