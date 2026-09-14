import { NextRequest, NextResponse } from "next/server";

const ADMIN_REFRESH_CSRF_COOKIE_NAME = "tpm_admin_refresh_csrf";
const CSRF_HEADER_NAME = "X-CSRF-Token";

const backendBaseURL = (process.env.BACKEND_URL || "http://localhost:8080").replace(
  /\/+$/,
  "",
);

const firstForwardedValue = (value: string | null): string | null =>
  value?.split(",", 1)[0]?.trim() || null;

const isSameOriginRequest = (request: NextRequest): boolean => {
  const origin = request.headers.get("origin");
  const host = firstForwardedValue(
    request.headers.get("x-forwarded-host") || request.headers.get("host"),
  );
  const protocol = firstForwardedValue(request.headers.get("x-forwarded-proto"));
  if (!origin || !host) {
    return false;
  }

  try {
    const parsedOrigin = new URL(origin);
    return (
      parsedOrigin.host === host &&
      (protocol === null || parsedOrigin.protocol === `${protocol}:`)
    );
  } catch {
    return false;
  }
};

export async function POST(request: NextRequest): Promise<NextResponse> {
  if (!isSameOriginRequest(request)) {
    return NextResponse.json(
      { title: "Недопустимый источник запроса", status: 403 },
      { status: 403 },
    );
  }

  const csrfToken = request.cookies.get(ADMIN_REFRESH_CSRF_COOKIE_NAME)?.value;
  const cookieHeader = request.headers.get("cookie");
  if (!csrfToken || !cookieHeader) {
    return NextResponse.json(
      { title: "Сессия администратора не найдена", status: 401 },
      { status: 401 },
    );
  }

  const backendResponse = await fetch(`${backendBaseURL}/api/v1/admin/logout`, {
    method: "POST",
    cache: "no-store",
    headers: {
      Cookie: cookieHeader,
      Origin: request.headers.get("origin") || request.nextUrl.origin,
      [CSRF_HEADER_NAME]: csrfToken,
    },
  });

  const response = new NextResponse(null, { status: backendResponse.status });
  for (const cookie of backendResponse.headers.getSetCookie()) {
    response.headers.append("Set-Cookie", cookie);
  }
  return response;
}
