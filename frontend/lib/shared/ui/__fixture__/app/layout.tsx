import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Стенд общих компонентов",
};

export default function FixtureLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru" data-theme="dark">
      <body>{children}</body>
    </html>
  );
}
