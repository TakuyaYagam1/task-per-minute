export const runtime = "nodejs";
export const dynamic = "force-static";

export async function GET() {
  return Response.json({ status: "ok" }, { status: 200 });
}
