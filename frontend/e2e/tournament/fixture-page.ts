const escapeAttribute = (value: string): string => value
  .replaceAll("&", "&amp;")
  .replaceAll('"', "&quot;")
  .replaceAll("<", "&lt;")
  .replaceAll(">", "&gt;");

export const tournamentFixturePage = (baseURL = "/"): string => {
  const baseHref = escapeAttribute(baseURL.endsWith("/") ? baseURL : `${baseURL}/`);

  return `<!doctype html>
<html lang="ru" data-theme="dark">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="color-scheme" content="dark light">
    <base href="${baseHref}">
    <title>Изолированный контур турнира</title>
    <style>
      :root {
        --bg: #101419;
        --panel: #1a2027;
        --panel-strong: #202832;
        --panel-muted: #151b21;
        --text: #f2f5f7;
        --muted: #a8b3be;
        --subtle: #8996a4;
        --border: #303a44;
        --border-strong: #455362;
        --accent: #70b5e8;
        --accent-strong: #9bd2f5;
        --accent-soft: #23394b;
        --success: #76c893;
        --warning: #e4b56c;
        --danger: #ef8e8e;
        --shadow: 0 14px 34px rgba(0, 0, 0, 0.2);
        --radius: 12px;
        --motion: 160ms ease-out;
      }

      html[data-theme="light"] {
        --bg: #f5f7fa;
        --panel: #ffffff;
        --panel-strong: #f0f4f8;
        --panel-muted: #f7f9fb;
        --text: #18212b;
        --muted: #526173;
        --subtle: #607084;
        --border: #d7e0e8;
        --border-strong: #b9c7d4;
        --accent: #175cd3;
        --accent-strong: #124bb0;
        --accent-soft: #e6efff;
        --success: #1d7a46;
        --warning: #9a5f0a;
        --danger: #b42318;
        --shadow: 0 14px 34px rgba(24, 33, 43, 0.1);
      }

      * { box-sizing: border-box; }

      html {
        min-width: 320px;
        background: var(--bg);
      }

      body {
        min-width: 320px;
        min-height: 100vh;
        margin: 0;
        overflow-x: hidden;
        background: var(--bg);
        color: var(--text);
        font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        font-size: 15px;
        line-height: 1.5;
        font-variant-numeric: tabular-nums;
        transition: background-color var(--motion), color var(--motion);
      }

      button {
        color: inherit;
        font: inherit;
      }

      button:focus-visible {
        outline: 2px solid var(--accent);
        outline-offset: 3px;
      }

      .shell {
        width: min(calc(100% - 32px), 1240px);
        margin: 0 auto;
        padding-bottom: 48px;
      }

      .masthead {
        min-height: 66px;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 20px;
        border-bottom: 1px solid var(--border);
      }

      .brand {
        display: inline-flex;
        align-items: center;
        gap: 10px;
        color: var(--text);
        font-size: 0.78rem;
        font-weight: 800;
        letter-spacing: 0.1em;
        text-transform: uppercase;
      }

      .mark {
        display: inline-grid;
        width: 28px;
        height: 28px;
        place-items: center;
        border: 1px solid var(--accent);
        border-radius: 7px;
        color: var(--accent);
        font-size: 0.63rem;
        letter-spacing: -0.04em;
      }

      .theme-switch {
        display: inline-flex;
        gap: 2px;
        padding: 3px;
        border: 1px solid var(--border);
        border-radius: 9px;
        background: var(--panel-muted);
      }

      .theme-button,
      .action-button {
        border: 1px solid transparent;
        border-radius: 7px;
        background: transparent;
        cursor: pointer;
        transition: background-color var(--motion), border-color var(--motion), color var(--motion), transform var(--motion);
      }

      .theme-button {
        min-height: 29px;
        padding: 0 10px;
        color: var(--muted);
        font-size: 0.73rem;
        font-weight: 750;
      }

      .theme-button[aria-pressed="true"] {
        background: var(--panel-strong);
        color: var(--text);
      }

      .theme-button:hover,
      .action-button:hover {
        color: var(--accent-strong);
      }

      .page-heading {
        display: flex;
        align-items: flex-end;
        justify-content: space-between;
        gap: 28px;
        padding: 42px 0 28px;
      }

      h1,
      h2,
      h3,
      p { margin: 0; }

      h1 {
        max-width: 730px;
        font-size: clamp(2.15rem, 5.2vw, 4.1rem);
        font-weight: 820;
        letter-spacing: -0.05em;
        line-height: 0.98;
      }

      .heading-copy {
        max-width: 630px;
        color: var(--muted);
        font-size: 0.96rem;
      }

      .heading-copy strong { color: var(--text); }

      .heading-meta {
        display: flex;
        flex-wrap: wrap;
        justify-content: flex-end;
        gap: 8px;
        color: var(--subtle);
        font-size: 0.75rem;
        text-align: right;
      }

      .meta-chip {
        display: inline-flex;
        align-items: center;
        gap: 7px;
        border: 1px solid var(--border);
        border-radius: 999px;
        padding: 5px 10px;
        white-space: nowrap;
      }

      .dot {
        width: 7px;
        height: 7px;
        flex: 0 0 auto;
        border-radius: 50%;
        background: var(--success);
      }

      .workspace {
        display: grid;
        grid-template-columns: minmax(0, 1.35fr) minmax(280px, 0.65fr);
        align-items: start;
        gap: 20px;
      }

      .panel {
        min-width: 0;
        border: 1px solid var(--border);
        border-radius: var(--radius);
        background: var(--panel);
        box-shadow: var(--shadow);
      }

      .roles-panel { overflow: hidden; }

      .panel-header {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 18px;
        padding: 22px 24px 18px;
        border-bottom: 1px solid var(--border);
      }

      .panel-header h2,
      .event-panel h2 {
        font-size: 1.03rem;
        font-weight: 780;
        letter-spacing: -0.018em;
      }

      .panel-header p,
      .event-panel p {
        margin-top: 5px;
        color: var(--muted);
        font-size: 0.79rem;
      }

      .role-row {
        display: grid;
        grid-template-columns: minmax(135px, 0.38fr) minmax(0, 1fr);
        gap: 22px;
        padding: 23px 24px;
        border-bottom: 1px solid var(--border);
      }

      .role-row:last-child { border-bottom: 0; }

      .role-title {
        display: flex;
        align-items: flex-start;
        gap: 10px;
      }

      .role-index {
        display: inline-grid;
        width: 23px;
        height: 23px;
        flex: 0 0 auto;
        place-items: center;
        border: 1px solid var(--border-strong);
        border-radius: 6px;
        color: var(--accent);
        font-size: 0.68rem;
        font-weight: 800;
      }

      .role-title h3 {
        font-size: 1.05rem;
        font-weight: 780;
      }

      .role-title p {
        margin-top: 4px;
        color: var(--muted);
        font-size: 0.76rem;
      }

      .role-content { min-width: 0; }

      .endpoint {
        overflow-wrap: anywhere;
        margin-bottom: 13px;
        color: var(--subtle);
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 0.7rem;
      }

      .button-row {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
      }

      .action-button {
        min-height: 34px;
        padding: 6px 11px;
        border-color: var(--border-strong);
        background: var(--panel-muted);
        color: var(--text);
        font-size: 0.76rem;
        font-weight: 700;
      }

      .action-button.primary {
        border-color: var(--accent);
        background: var(--accent-soft);
        color: var(--accent-strong);
      }

      .action-button:active { transform: translateY(1px); }

      .role-status {
        min-height: 24px;
        margin-top: 12px;
        color: var(--muted);
        font-size: 0.78rem;
      }

      .role-status.success { color: var(--success); }
      .role-status.error { color: var(--danger); }

      .event-panel {
        padding: 22px;
        position: sticky;
        top: 20px;
      }

      .event-box {
        min-height: 160px;
        display: flex;
        align-items: flex-start;
        margin-top: 19px;
        padding: 14px;
        border: 1px solid var(--border);
        border-radius: 9px;
        background: var(--panel-muted);
        color: var(--muted);
        font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
        font-size: 0.72rem;
        line-height: 1.55;
        white-space: pre-wrap;
        overflow-wrap: anywhere;
      }

      .event-box[data-state="error"] { color: var(--danger); }
      .event-box[data-state="success"] { color: var(--success); }

      .contract-note {
        display: grid;
        gap: 10px;
        margin-top: 20px;
        padding-top: 18px;
        border-top: 1px solid var(--border);
        color: var(--muted);
        font-size: 0.77rem;
      }

      .contract-note strong { color: var(--text); }

      @media (max-width: 900px) {
        .page-heading { align-items: flex-start; flex-direction: column; }
        .heading-meta { justify-content: flex-start; text-align: left; }
        .workspace { grid-template-columns: 1fr; }
        .event-panel { position: static; }
      }

      @media (max-width: 620px) {
        .shell { width: min(calc(100% - 22px), 1240px); }
        .masthead { min-height: 58px; }
        .brand { font-size: 0.69rem; letter-spacing: 0.07em; }
        .mark { width: 25px; height: 25px; }
        .page-heading { padding: 30px 0 22px; gap: 17px; }
        h1 { font-size: clamp(2rem, 12vw, 3.1rem); }
        .panel-header,
        .role-row { padding-left: 17px; padding-right: 17px; }
        .role-row { grid-template-columns: 1fr; gap: 15px; }
        .event-panel { padding: 18px; }
        .theme-button { padding: 0 8px; }
      }
    </style>
  </head>
  <body>
    <main class="shell">
      <header class="masthead">
        <div class="brand"><span class="mark">TPM</span><span>Контур турнира</span></div>
        <div class="theme-switch" aria-label="Тема">
          <button class="theme-button" type="button" data-theme="dark" aria-pressed="true">Темная</button>
          <button class="theme-button" type="button" data-theme="light" aria-pressed="false">Светлая</button>
        </div>
      </header>

      <section class="page-heading" aria-labelledby="page-title">
        <div>
          <h1 id="page-title">Один контракт.<br>Три границы.</h1>
          <p class="heading-copy">Изолированный браузерный контур для <strong>participant</strong>, <strong>operator</strong> и <strong>public</strong>. Все ответы синтетические и приходят через контролируемую сеть.</p>
        </div>
        <div class="heading-meta">
          <span class="meta-chip"><span class="dot"></span>loopback only</span>
          <span class="meta-chip">rev 9 / cursor 14</span>
        </div>
      </section>

      <div class="workspace">
        <section class="panel roles-panel" aria-labelledby="roles-title">
          <div class="panel-header">
            <div>
              <h2 id="roles-title">Проверка ролей</h2>
              <p>REST и realtime вызываются из браузера; ответы задает только Playwright.</p>
            </div>
          </div>

          <section class="role-row" data-role="participant" aria-labelledby="participant-title">
            <div class="role-title">
              <span class="role-index">01</span>
              <div><h3 id="participant-title">Participant</h3><p>лобби, assignment, Golden</p></div>
            </div>
            <div class="role-content">
              <p class="endpoint">GET /api/v1/tournaments/{id}/participant/*</p>
              <div class="button-row">
                <button class="action-button primary" type="button" data-role-action="participant-session">Показать сессию</button>
                <button class="action-button" type="button" data-role-action="participant-golden">Показать Golden</button>
                <button class="action-button" type="button" data-role-action="participant-error">Проверить HTTP 409</button>
                <button class="action-button" type="button" data-role-action="participant-realtime">Открыть realtime</button>
              </div>
              <p class="role-status" data-role-status="participant" aria-live="polite">Ожидание</p>
            </div>
          </section>

          <section class="role-row" data-role="operator" aria-labelledby="operator-title">
            <div class="role-title">
              <span class="role-index">02</span>
              <div><h3 id="operator-title">Operator</h3><p>BO1, BO3, pause graph</p></div>
            </div>
            <div class="role-content">
              <p class="endpoint">GET /api/v1/admin/tournaments/{id}/*</p>
              <div class="button-row">
                <button class="action-button primary" type="button" data-role-action="operator-session">Показать пульт</button>
                <button class="action-button" type="button" data-role-action="operator-realtime">Открыть realtime</button>
              </div>
              <p class="role-status" data-role-status="operator" aria-live="polite">Ожидание</p>
            </div>
          </section>

          <section class="role-row" data-role="public" aria-labelledby="public-title">
            <div class="role-title">
              <span class="role-index">03</span>
              <div><h3 id="public-title">Public</h3><p>проекция без private fields</p></div>
            </div>
            <div class="role-content">
              <p class="endpoint">GET /api/v1/tournaments/{id}/{projection}</p>
              <div class="button-row">
                <button class="action-button primary" type="button" data-role-action="public-projection">Показать проекцию</button>
                <button class="action-button" type="button" data-role-action="public-realtime">Открыть realtime</button>
              </div>
              <p class="role-status" data-role-status="public" aria-live="polite">Ожидание</p>
            </div>
          </section>
        </section>

        <aside class="panel event-panel" aria-labelledby="event-title">
          <h2 id="event-title">Последнее событие</h2>
          <p>Состояние последнего browser вызова.</p>
          <output class="event-box" data-event-output="true" data-state="idle" aria-label="Результат вызова">Ожидание</output>
          <div class="contract-note">
            <p><strong>Данные</strong><br>generated DTO, ISO dates, server revision 9</p>
            <p><strong>Ошибки</strong><br>HTTP 409 и WS close 1013 идут по управляемой ветке</p>
          </div>
        </aside>
      </div>
    </main>

    <script>
      (() => {
        const tournamentId = "00000000-0000-4000-8000-000000000101";
        const assignmentId = "00000000-0000-4000-8000-000000000180";
        const resumeId = "00000000-0000-4000-8000-000000000191";
        const paths = {
          participantLobby: "/api/v1/tournaments/" + tournamentId + "/participant/lobby",
          participantAssignment: "/api/v1/tournaments/" + tournamentId + "/participant/assignments/" + assignmentId,
          participantSnapshot: "/api/v1/tournaments/" + tournamentId + "/participant/snapshot",
          participantGolden: "/api/v1/tournaments/" + tournamentId + "/participant/golden",
          operatorSnapshot: "/api/v1/admin/tournaments/" + tournamentId + "/snapshot",
          operatorGolden: "/api/v1/admin/tournaments/" + tournamentId + "/golden",
          publicTournament: "/api/v1/tournaments/" + tournamentId,
          publicScoreboard: "/api/v1/tournaments/" + tournamentId + "/scoreboard",
          publicBracket: "/api/v1/tournaments/" + tournamentId + "/bracket"
        };
        const output = document.querySelector("[data-event-output]");

        const statusNode = (role) => document.querySelector("[data-role-status='" + role + "']");
        const setStatus = (role, message, state) => {
          const node = statusNode(role);
          if (node) {
            node.textContent = message;
            node.className = "role-status" + (state ? " " + state : "");
          }
          if (output) {
            output.textContent = message;
            output.dataset.state = state || "idle";
          }
        };
        const requestJSON = async (path) => {
          const response = await fetch(path, { credentials: "include" });
          if (!response.ok) {
            throw new Error("HTTP " + response.status);
          }
          return response.json();
        };
        const run = async (role, label, requests) => {
          setStatus(role, label + "...", "");
          try {
            const values = await Promise.all(requests.map(requestJSON));
            const summary = label + ": " + values.length + " ответ(а), revision 9";
            setStatus(role, summary, "success");
          } catch (error) {
            setStatus(role, label + ": " + (error instanceof Error ? error.message : "ошибка"), "error");
          }
        };
        const realtimePath = (role) => {
          if (role === "participant") {
            return "/api/v1/tournaments/" + tournamentId + "/participant/realtime";
          }
          if (role === "operator") {
            return "/api/v1/admin/tournaments/" + tournamentId + "/realtime";
          }
          return "/api/v1/tournaments/" + tournamentId + "/realtime";
        };
        const openRealtime = (role) => {
          setStatus(role, "realtime подключается...", "");
          const socketURL = new URL(realtimePath(role), document.baseURI);
          socketURL.protocol = socketURL.protocol === "https:" ? "wss:" : "ws:";
          socketURL.searchParams.set("resume_id", resumeId);
          const socket = new WebSocket(socketURL.toString());
          socket.addEventListener("open", () => setStatus(role, "realtime подключен", "success"));
          socket.addEventListener("message", (event) => {
            try {
              JSON.parse(String(event.data));
              setStatus(role, "realtime кадр получен", "success");
            } catch {
              setStatus(role, "WS ошибка: некорректный кадр", "error");
            }
          });
          socket.addEventListener("error", () => setStatus(role, "WS ошибка", "error"));
          socket.addEventListener("close", (event) => {
            if (event.code === 1000) {
              setStatus(role, "realtime закрыт", "");
            } else {
              setStatus(role, "WS ошибка: закрытие " + event.code, "error");
            }
          });
        };

        document.querySelectorAll("[data-theme]").forEach((button) => {
          button.addEventListener("click", () => {
            const theme = button.getAttribute("data-theme");
            if (theme !== "dark" && theme !== "light") return;
            document.documentElement.dataset.theme = theme;
            document.querySelectorAll("[data-theme]").forEach((candidate) => {
              candidate.setAttribute("aria-pressed", String(candidate === button));
            });
          });
        });

        document.querySelectorAll("[data-role-action]").forEach((button) => {
          button.addEventListener("click", () => {
            const action = button.getAttribute("data-role-action");
            if (action === "participant-session") {
              void run("participant", "Participant session", [paths.participantLobby, paths.participantAssignment, paths.participantSnapshot]);
            } else if (action === "participant-golden") {
              void run("participant", "Participant Golden", [paths.participantGolden]);
            } else if (action === "participant-error") {
              void run("participant", "Participant conflict", [paths.participantSnapshot]);
            } else if (action === "operator-session") {
              void run("operator", "Operator console", [paths.operatorSnapshot, paths.operatorGolden]);
            } else if (action === "public-projection") {
              void run("public", "Public projection", [paths.publicTournament, paths.publicScoreboard, paths.publicBracket]);
            } else if (action === "participant-realtime") {
              openRealtime("participant");
            } else if (action === "operator-realtime") {
              openRealtime("operator");
            } else if (action === "public-realtime") {
              openRealtime("public");
            }
          });
        });
      })();
    </script>
  </body>
</html>`;
};
