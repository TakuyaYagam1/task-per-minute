import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Контракт API оператора",
};

export default function OperatorApiLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ru">
      <body>{children}</body>
    </html>
  );
}
