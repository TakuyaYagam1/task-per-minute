export const runtime = "nodejs";

export async function POST(request: Request) {
  try {
    await request.text();
  } catch {
    // CSP reports are best-effort telemetry; malformed bodies should not fail the page.
  }

  return new Response(null, { status: 204 });
}
