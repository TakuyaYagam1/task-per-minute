"use client";

import { useEffect, useRef, useState } from "react";

import {
  Button,
  Dialog,
  Message,
  Panel,
  Status,
  Table,
  Tabs,
} from "../../index";

import "./fixture.css";

type Theme = "dark" | "light";
type TableMode = "ready" | "loading" | "empty" | "error";

interface Participant {
  id: string;
  name: string;
  score: number;
}

const participants: readonly Participant[] = [
  { id: "p-01", name: "Лиса", score: 12 },
  { id: "p-02", name: "Сова", score: 9 },
];

const participantColumns = [
  { key: "name", header: "Участник", accessor: "name" as const },
  { key: "score", header: "Очки", accessor: "score" as const, align: "right" as const },
];

export default function SharedUiFixture() {
  const [hydrated, setHydrated] = useState(false);
  const [theme, setTheme] = useState<Theme>("dark");
  const [buttonLoading, setButtonLoading] = useState(false);
  const [tableMode, setTableMode] = useState<TableMode>("ready");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [dialogEvents, setDialogEvents] = useState<string[]>([]);
  const dialogTriggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    setHydrated(true);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <main className="fixture-shell motion-page" data-hydrated={hydrated}>
      <h1>Общие компоненты интерфейса</h1>
      <p className="fixture-lede">
        Изолированный экран с синтетическими данными для проверки доступности и состояний.
      </p>

      <Panel
        title="Стенд компонентов"
        description="Семантические общие компоненты на одной странице."
        footer={<Status tone="success">Стенд готов</Status>}
      >
        <div className="fixture-stack">
          <div className="fixture-actions" role="group" aria-label="Тема оформления">
            <Button
              variant="secondary"
              aria-pressed={theme === "dark"}
              onClick={() => setTheme("dark")}
            >
              Темная тема
            </Button>
            <Button
              variant="secondary"
              aria-pressed={theme === "light"}
              onClick={() => setTheme("light")}
            >
              Светлая тема
            </Button>
          </div>

          <div className="fixture-actions" role="group" aria-label="Состояния кнопки">
            <Button
              loading={buttonLoading}
              loadingLabel="Действие выполняется"
              onClick={() => setButtonLoading((current) => !current)}
            >
              Запустить действие
            </Button>
            <Button disabled variant="secondary">
              Недоступное действие
            </Button>
          </div>
        </div>
      </Panel>

      <div className="fixture-grid">
        <Panel className="fixture-card" title="Статусы" tone="muted">
          <div className="fixture-stack">
            <Status tone="neutral">Ожидание</Status>
            <Status tone="loading">Загрузка данных</Status>
            <Status tone="empty">Нет участников</Status>
            <Status tone="disabled">Недоступно</Status>
          </div>
        </Panel>

        <Panel className="fixture-card" title="Сообщения" tone="muted">
          <div className="fixture-stack">
            <Message tone="loading">Данные загружаются</Message>
            <Message tone="empty">Список пока пуст</Message>
            <Message tone="error" title="Ошибка загрузки">
              Не удалось обновить список.
            </Message>
          </div>
        </Panel>
      </div>

      <Panel title="Навигация" description="Стрелки, Home и End меняют активную вкладку.">
        <Tabs
          ariaLabel="Разделы стенда"
          items={[
            { id: "summary", label: "Сводка", panel: "Сводка турнира" },
            { id: "ranking", label: "Рейтинг", panel: "Рейтинг участников" },
            { id: "rounds", label: "Раунды", panel: "Раунды турнира" },
            { id: "archive", label: "Архив", panel: "Архив недоступен", disabled: true },
          ]}
        />
      </Panel>

      <Panel title="Таблица участников" description="Таблица показывает явные состояния загрузки, пустого списка и ошибки.">
        <div className="fixture-stack">
          <div className="fixture-actions" role="group" aria-label="Состояние таблицы">
            <Button variant="ghost" onClick={() => setTableMode("ready")}>Данные</Button>
            <Button variant="ghost" onClick={() => setTableMode("loading")}>Загрузка</Button>
            <Button variant="ghost" onClick={() => setTableMode("empty")}>Пусто</Button>
            <Button variant="ghost" onClick={() => setTableMode("error")}>Ошибка</Button>
          </div>
          <div className="fixture-table-wrap">
            <Table
              ariaLabel="Таблица результатов"
              caption="Результаты раунда"
              columns={participantColumns}
              rows={tableMode === "ready" ? participants : []}
              loading={tableMode === "loading"}
              error={tableMode === "error" ? "Сервер результатов недоступен" : undefined}
              empty={tableMode === "empty" ? "В этом раунде пока нет участников" : undefined}
              rowKey="id"
            />
          </div>
        </div>
      </Panel>

      <Panel title="Диалог" description="Модальное окно проверяет Escape, фокус и возврат фокуса.">
        <Button ref={dialogTriggerRef} onClick={() => setDialogOpen(true)}>
          Открыть диалог
        </Button>
        <output aria-label="События диалога">{dialogEvents.join(",") || "Нет"}</output>
        <Dialog
          className="fixture-dialog"
          open={dialogOpen}
          title="Подтвердить действие"
          description="Фокус остается внутри окна, пока оно открыто."
          closeLabel="Закрыть диалог"
          onOpenChange={(nextOpen) => {
            setDialogEvents((events) => [...events, `open:${nextOpen}`]);
            setDialogOpen(nextOpen);
          }}
          onClose={() => setDialogEvents((events) => [...events, "close"])}
          onCancel={() => setDialogEvents((events) => [...events, "cancel"])}
          returnFocusRef={dialogTriggerRef}
          footer={
            <Button variant="secondary" onClick={() => setDialogOpen(false)}>
              Отмена
            </Button>
          }
        >
          <div className="fixture-stack">
            <p>Синтетическая запись готова к проверке.</p>
            <Button onClick={() => setDialogOpen(false)}>Подтвердить</Button>
          </div>
        </Dialog>
      </Panel>
    </main>
  );
}
