import type { NextConfig } from "next";

const trimTrailingSlash = (value: string): string => value.replace(/\/+$/, "");

const buildConnectSrc = (): string => {
  const sources = new Set<string>(["'self'"]);
  const apiUrl = process.env.NEXT_PUBLIC_API_URL?.trim();
  const adminApiUrl = process.env.NEXT_PUBLIC_ADMIN_API_URL?.trim();
  const wsUrl = process.env.NEXT_PUBLIC_WS_URL?.trim();

  for (const httpUrl of [apiUrl, adminApiUrl]) {
    if (!httpUrl) continue;
    try {
      const parsed = new URL(httpUrl);
      sources.add(`${parsed.protocol}//${parsed.host}`);
    } catch {
      // Ignore malformed env values; CSP just stays tighter.
    }
  }

  if (wsUrl) {
    try {
      const parsed = new URL(wsUrl);
      sources.add(`${parsed.protocol}//${parsed.host}`);
    } catch {
      // Ignore malformed env value.
    }
  } else if (apiUrl) {
    try {
      const parsed = new URL(apiUrl);
      const wsScheme = parsed.protocol === "https:" ? "wss:" : "ws:";
      sources.add(`${wsScheme}//${parsed.host}`);
    } catch {
      // Ignore.
    }
  }

  return Array.from(sources).join(" ");
};

const buildCSP = (isProduction: boolean): string => {
  const connectSrc = buildConnectSrc();
  const directives: string[] = [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data: https://fonts.gstatic.com",
    `connect-src ${connectSrc}`,
    "worker-src 'self' blob:",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "object-src 'none'",
    "report-uri /csp-report",
  ];
  if (isProduction) {
    directives.push("upgrade-insecure-requests");
  }
  return directives.join("; ");
};

const isProduction = process.env.NODE_ENV === "production";

const securityHeaders = [
  {
    key: "Strict-Transport-Security",
    value: "max-age=63072000; includeSubDomains; preload",
  },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Permissions-Policy",
    value: "camera=(), microphone=(), geolocation=(), interest-cohort=()",
  },
  { key: "Cross-Origin-Opener-Policy", value: "same-origin" },
  {
    key: isProduction ? "Content-Security-Policy" : "Content-Security-Policy-Report-Only",
    value: buildCSP(isProduction),
  },
];

const backendBase = process.env.BACKEND_URL
  ? trimTrailingSlash(process.env.BACKEND_URL)
  : "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  outputFileTracingRoot: process.cwd(),
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${backendBase}/api/:path*`,
      },
      {
        source: "/ws",
        destination: `${backendBase}/ws`,
      },
    ];
  },
  async headers() {
    return [
      {
        source: "/:path*",
        headers: securityHeaders,
      },
    ];
  },
};

export default nextConfig;
