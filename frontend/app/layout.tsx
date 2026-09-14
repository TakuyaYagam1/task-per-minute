import "./globals.css";
import { ThemeToggle } from "@/shared/ui";

const themeStorageKey = "task-per-minute-theme";
const themeBootstrapScript = `
(function () {
  var root = document.documentElement;
  var theme = "dark";

  try {
    var storedTheme = window.localStorage.getItem(${JSON.stringify(themeStorageKey)});
    if (storedTheme === "dark" || storedTheme === "light") {
      theme = storedTheme;
    }
  } catch {}

  root.dataset.theme = theme;
  root.style.colorScheme = theme;
})();
`;

export const metadata = {
  title: "Task Per Minute",
  description: "Платформа турниров CTF",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="ru" data-theme="dark" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeBootstrapScript }} />
      </head>
      <body>
        <ThemeToggle storageKey={themeStorageKey} />
        {children}
      </body>
    </html>
  );
}
