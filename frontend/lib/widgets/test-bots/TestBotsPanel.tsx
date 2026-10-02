"use client";

import { useEffect, useRef, useState } from "react";
import { playerModel } from "../../entities/player";
import { botAction, getBotAnswer, getBotState, type BotAction, type BotPolicy, type BotView } from "../../shared/api/test-bots";
import { Button } from "../../shared/ui";
import styles from "./TestBotsPanel.module.css";

export function TestBotsPanel({ tournamentId }: Readonly<{ tournamentId: string }>) {
  const [view, setView] = useState<BotView | null>(null);
  const [scenario, setScenario] = useState<"free" | "golden">("free");
  const [policy, setPolicy] = useState<BotPolicy>({ outcome: "human_wins", delay: 15 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [answer, setAnswer] = useState("");
  const request = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const actionVersion = useRef(0);
  useEffect(() => {
    const controller = new AbortController(); let timer: ReturnType<typeof setTimeout>;
    const current = ++generation.current;
    let hydrated = false;
    request.current?.abort(); request.current = null; actionVersion.current++;
    setView(null); setAnswer(""); setError(""); setBusy(false);
    const refresh = async () => {
      const version = actionVersion.current;
      try {
        const state = await getBotState(tournamentId, controller.signal);
        if (generation.current !== current || controller.signal.aborted) return;
        if (version === actionVersion.current && !request.current) {
          setView(state);
          if (state && !hydrated) { setPolicy(state.next); hydrated = true; }
        }
        if (!state) { setAnswer(""); return; }
      }
      catch { if (controller.signal.aborted) return; }
      timer = setTimeout(() => void refresh(), 5000);
    };
    const unsubscribe = playerModel.subscribeAvatarChanges((change) => { if (change === "clear") { controller.abort(); request.current?.abort(); generation.current++; setAnswer(""); setView(null); } });
    void refresh();
    return () => { controller.abort(); request.current?.abort(); clearTimeout(timer); unsubscribe(); };
  }, [tournamentId]);
  useEffect(() => { if (!answer) return; const timer = setTimeout(() => setAnswer(""), 30000); return () => clearTimeout(timer); }, [answer]);
  async function act(action?: BotAction) {
    const controller = new AbortController(); request.current?.abort(); request.current = controller;
    actionVersion.current++;
    const current = generation.current; setBusy(true); setError(""); setAnswer("");
    try {
      if (action) { const next = await botAction(tournamentId, action, controller.signal); if (current === generation.current && !controller.signal.aborted) setView(next); }
      else { const text = await getBotAnswer(tournamentId, controller.signal); if (current === generation.current && !controller.signal.aborted) setAnswer(text); }
    } catch (e) { if (!controller.signal.aborted && current === generation.current) setError(e instanceof Error ? e.message : "Не удалось выполнить действие."); }
    finally { if (current === generation.current && request.current === controller) { request.current = null; setBusy(false); } }
  }
  if (!view) return null;
  const active = view.status === "running" || view.status === "paused";
  return <details className={styles.panel}>
    <summary>Тестирование</summary>
    <p role="status">{view.message}</p>
    <div className={styles.controls}>
      {!active && view.status !== "busy" && <>
        <label>Сценарий<select value={scenario} onChange={(event) => setScenario(event.target.value as "free" | "golden")}><option value="free">Свободная игра</option><option value="golden">Пройти Golden</option></select></label>
        <Button loading={busy} onClick={() => void act({ action: "start", scenario })}>Заполнить ботами</Button>
      </>}
      {active && <>
        <>
          <label>Следующая серия<select value={policy.outcome} onChange={(event) => setPolicy({ ...policy, outcome: event.target.value as BotPolicy["outcome"] })}><option value="human_wins">Дать мне выиграть</option><option value="bot_wins">Соперник выигрывает</option></select></label>
          <label>Задержка, с<input type="number" min={2} max={120} value={policy.delay} onChange={(event) => setPolicy({ ...policy, delay: Number(event.target.value) })} /></label>
          <Button variant="secondary" loading={busy} onClick={() => void act({ action: "configure", ...policy })}>Применить к следующей серии</Button>
        </>
        <Button variant="secondary" loading={busy} onClick={() => void act({ action: view.status === "paused" ? "resume" : "pause" })}>{view.status === "paused" ? "Продолжить" : "Приостановить"}</Button>
        <Button variant="secondary" loading={busy} onClick={() => void act()}>Показать тестовый ответ</Button>
      </>}
    </div>
    {active && view.scenario === "golden" && <p>В Swiss результаты задает сценарий Golden. Выбор следующей серии действует в плей-офф.</p>}
    {answer && <div className={styles.controls}><code className={styles.answer}>{answer}</code><Button variant="secondary" onClick={() => void navigator.clipboard.writeText(answer).catch(() => setError("Не удалось скопировать. Выделите ответ вручную."))}>Скопировать</Button></div>}
    {error && <p role="alert" className={styles.error}>{error}</p>}
    {view.bots.length > 0 && <><p>Участники: {1 + view.bots.filter((bot) => bot.participant_id).length} из {view.size}</p><ul className={styles.bots}>{view.bots.map((bot) => <li key={bot.player_id}><strong>{bot.username}</strong><span>{bot.message}</span></li>)}</ul></>}
  </details>;
}
