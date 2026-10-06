import "./globals.css";
import localFont from "next/font/local";
import { FspHeader } from "../lib/shared/ui/FspHeader";

const interTight = localFont({
  src: [
    { path: "../public/fonts/InterTight-Regular.woff2", weight: "400" },
    { path: "../public/fonts/InterTight-Medium.woff2", weight: "500" },
    { path: "../public/fonts/InterTight-SemiBold.woff2", weight: "600" },
  ],
  variable: "--font-brand",
  display: "swap",
});

const spectral = localFont({
  src: [
    { path: "../public/fonts/Spectral-Regular.woff2", weight: "400" },
    { path: "../public/fonts/Spectral-Medium.woff2", weight: "500" },
  ],
  variable: "--font-display",
  display: "swap",
});

export const metadata = {
  title: "Task Per Minute",
  description: "CTF-дуэли Task Per Minute / Финал Кубка Федерации",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="ru">
      <body className={`${interTight.variable} ${spectral.variable}`}>
        <FspHeader />
        {children}
      </body>
    </html>
  );
}
