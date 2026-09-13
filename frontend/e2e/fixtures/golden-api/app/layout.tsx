import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Контракт Golden",
};

export default function GoldenApiLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru">
      <body>{children}</body>
    </html>
  );
}
