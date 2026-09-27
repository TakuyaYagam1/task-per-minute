import "./globals.css";
import { SiteHeaderAuthProvider } from "@/features/site-header";
import { BackToTop } from "@/widgets/back-to-top";
import { SiteHeader } from "@/widgets/site-header";

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
  description: "Платформа соревнований CTF",
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
        <SiteHeaderAuthProvider>
          <SiteHeader />
          <BackToTop />
          {children}
        </SiteHeaderAuthProvider>
      </body>
    </html>
  );
}
