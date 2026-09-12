import { expect, test } from "@playwright/test";

import {
  ApiError,
  getTournamentContent,
} from "../lib/shared/api";
import type { components } from "../lib/shared/api/schema";

type TournamentContentSelection = components["schemas"]["TournamentContentSelection"];
type FetchHandler = (request: Request, calls: readonly Request[]) => Response | Promise<Response>;

const contentSelection = (): TournamentContentSelection => ({
  content_revision: 17,
  publication_id: "11111111-1111-4111-8111-111111111111",
  published_at: "2026-09-12T08:00:00Z",
  normal_pool_revision_id: "22222222-2222-4222-8222-222222222222",
  golden_pool_revision_id: "33333333-3333-4333-8333-333333333333",
});

const jsonResponse = (
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): Response => {
  const encoded = JSON.stringify(body);
  return new Response(encoded === undefined ? "" : encoded, {
    status,
    headers: {
      "content-type": "application/json",
      ...headers,
    },
  });
};

const withFetchStub = async <T>(
  handler: FetchHandler,
  run: (calls: Request[]) => Promise<T>,
): Promise<T> => {
  const originalFetch = globalThis.fetch;
  const calls: Request[] = [];
  globalThis.fetch = async (input, init) => {
    const request = new Request(input, init);
    calls.push(request);
    return handler(request, calls);
  };

  try {
    return await run(calls);
  } finally {
    globalThis.fetch = originalFetch;
  }
};

test("gets the current tournament content selection through the admin client", async () => {
  const expected = contentSelection();

  const calls = await withFetchStub(
    (request) => {
      expect(request.method).toBe("GET");
      expect(new URL(request.url).pathname).toBe("/api/v1/admin/tournament-content");
      expect(request.credentials).toBe("include");
      expect(request.headers.get("authorization")).toBeNull();
      return jsonResponse(200, expected, { "cache-control": "no-store" });
    },
    async (requests) => {
      await expect(getTournamentContent()).resolves.toEqual(expected);
      return requests;
    },
  );

  expect(calls).toHaveLength(1);
});

test("preserves abort propagation for tournament content discovery", async () => {
  const controller = new AbortController();
  let requestSignal: AbortSignal | undefined;

  await withFetchStub(
    (request) => {
      requestSignal = request.signal;
      return new Promise<Response>((_resolve, reject) => {
        request.signal.addEventListener(
          "abort",
          () => reject(request.signal.reason),
          { once: true },
        );
      });
    },
    async () => {
      const pending = getTournamentContent(controller.signal);
      controller.abort();
      await expect(pending).rejects.toMatchObject({ name: "AbortError" });
      return undefined;
    },
  );

  expect(requestSignal?.aborted).toBe(true);
});

test("refreshes an expired admin session once and retries the discovery request", async () => {
  const expected = contentSelection();

  const calls = await withFetchStub(
    (request, requests) => {
      const path = new URL(request.url).pathname;
      if (requests.length === 1) {
        expect(path).toBe("/api/v1/admin/tournament-content");
        return jsonResponse(401, {
          type: "about:blank",
          title: "Unauthorized",
          status: 401,
          request_id: "request-expired",
        }, { "content-type": "application/problem+json" });
      }
      if (requests.length === 2) {
        expect(path).toBe("/api/v1/admin/refresh");
        expect(request.method).toBe("POST");
        expect(request.credentials).toBe("include");
        return jsonResponse(200, { expires_in: 3600 });
      }

      expect(requests.length).toBe(3);
      expect(path).toBe("/api/v1/admin/tournament-content");
      expect(request.method).toBe("GET");
      return jsonResponse(200, expected);
    },
    async (requests) => {
      await expect(getTournamentContent()).resolves.toEqual(expected);
      return requests;
    },
  );

  expect(calls).toHaveLength(3);
});

test("rejects malformed successful tournament content responses", async () => {
  const valid = contentSelection();
  const malformed: ReadonlyArray<{ name: string; value: unknown }> = [
    { name: "null body", value: null },
    { name: "missing field", value: { ...valid, golden_pool_revision_id: undefined } },
    { name: "unsafe revision", value: { ...valid, content_revision: Number.MAX_SAFE_INTEGER + 1 } },
    { name: "fractional revision", value: { ...valid, content_revision: 1.5 } },
    {
      name: "nil publication identifier",
      value: { ...valid, publication_id: "00000000-0000-0000-0000-000000000000" },
    },
    { name: "invalid pool identifier", value: { ...valid, normal_pool_revision_id: "pool" } },
    {
      name: "same pool identifiers",
      value: { ...valid, golden_pool_revision_id: valid.normal_pool_revision_id.toUpperCase() },
    },
    { name: "invalid timestamp", value: { ...valid, published_at: "not-a-timestamp" } },
    { name: "date without time", value: { ...valid, published_at: "2026-09-12" } },
    { name: "unexpected field", value: { ...valid, extra: true } },
  ];

  for (const variant of malformed) {
    await withFetchStub(
      () => jsonResponse(200, variant.value),
      async () => {
        await expect(getTournamentContent()).rejects.toThrow(
          "Invalid API response: admin/tournament-content",
        );
      },
    );
  }
});

test("preserves authorization, unavailable-content, and rate-limit errors", async () => {
  const cases = [
    { status: 403, title: "Forbidden", detail: "operator scope required", retryAfter: null },
    { status: 422, title: "Unprocessable Content", detail: "no usable publication", retryAfter: null },
    { status: 429, title: "Too Many Requests", detail: "retry later", retryAfter: "21" },
  ] as const;

  for (const failure of cases) {
    await withFetchStub(
      () => jsonResponse(
        failure.status,
        {
          type: "about:blank",
          title: failure.title,
          status: failure.status,
          detail: failure.detail,
          request_id: `request-${failure.status}`,
        },
        {
          "content-type": "application/problem+json",
          ...(failure.retryAfter === null ? {} : { "retry-after": failure.retryAfter }),
        },
      ),
      async (requests) => {
        const error = await getTournamentContent().catch((value: unknown) => value);
        expect(error).toBeInstanceOf(ApiError);
        if (!(error instanceof ApiError)) {
          return;
        }
        expect(error.status).toBe(failure.status);
        expect(error.problem?.request_id).toBe(`request-${failure.status}`);
        expect(error.retryAfter).toBe(failure.retryAfter);
        expect(error.message).toBe(failure.detail);
        expect(requests).toHaveLength(1);
      },
    );
  }
});
