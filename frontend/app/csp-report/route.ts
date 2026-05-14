export const runtime = "nodejs";

const maxCspReportBytes = 64 * 1024;

export async function POST(request: Request) {
  if (contentLengthExceedsLimit(request)) {
    return new Response(null, { status: 413 });
  }

  const readResult = await readBodyWithinLimit(request, maxCspReportBytes);
  if (readResult === "too_large") {
    return new Response(null, { status: 413 });
  }

  return new Response(null, { status: 204 });
}

function contentLengthExceedsLimit(request: Request): boolean {
  const raw = request.headers.get("content-length");
  if (raw === null) {
    return false;
  }
  const length = Number(raw);
  return Number.isFinite(length) && length > maxCspReportBytes;
}

async function readBodyWithinLimit(request: Request, limit: number): Promise<"ok" | "too_large"> {
  const reader = request.body?.getReader();
  if (!reader) {
    return "ok";
  }

  let total = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        return "ok";
      }
      total += value.byteLength;
      if (total > limit) {
        void reader.cancel().catch(() => undefined);
        return "too_large";
      }
    }
  } catch {
    // CSP reports are best-effort telemetry; malformed bodies should not fail the page.
    return "ok";
  }
}
