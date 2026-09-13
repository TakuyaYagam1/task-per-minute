import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Контракт публичного API",
};

export default function PublicApiLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru">
      <body>{children}</body>
    </html>
  );
}
