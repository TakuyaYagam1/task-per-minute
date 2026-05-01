"use client";

import React, { useEffect, useState, useRef } from "react";
import { useRouter } from "next/navigation";
import Image from "next/image";

import { WebSocketMessage } from "../../shared/types";
import { showNotification } from "../../shared/lib";
import { playerModel } from "../../entities/player";
import { gameModel } from "../../entities/game";
import { PlayButton, useWebSocket } from "../../features/game-queue";
import { WaitingOverlay } from "../../widgets/waiting-overlay";

export default function HomePage() {
  const router = useRouter();
  const [playerID, setPlayerID] = useState<number | null>(null);
  const [isWaiting, setIsWaiting] = useState(false);
  const [queueSize, setQueueSize] = useState<number | undefined>(undefined);
  const [notification, setNotification] = useState<string | null>(null);

  const { websocketRef, connectWebSocket, sendMessage } = useWebSocket();

  useEffect(() => {
    const initializePlayer = async () => {
      const player = await playerModel.initializePlayer();
      if (player) {
        setPlayerID(player.id);
        console.log("Player initialized:", player.id);
      } else {
        showNotification("Ошибка подключения к серверу", setNotification);
      }
    };

    initializePlayer();
  }, []);

  useEffect(() => {
    return () => {
      if (
        websocketRef.current &&
        websocketRef.current.readyState !== WebSocket.CLOSED
      ) {
        console.log("Closing WebSocket connection on main page unmount");
        websocketRef.current.close();
        websocketRef.current = null;
        window.gameWebSocket = undefined;
      }
    };
  }, [websocketRef]);

  const handleWebSocketMessage = (message: WebSocketMessage) => {
    switch (message.type) {
      case "ping":
        console.log("Received ping from server, sending pong");
        sendMessage("pong", message.data);
        break;

      case "pong":
        console.log("Received pong from server");
        break;

      case "join_queue":
        if (message.data?.queue_size !== undefined) {
          setQueueSize(message.data.queue_size);
        }
        showNotification("Вы добавлены в очередь", setNotification);
        break;

      case "match_found":
        console.log("Match found!");
        showNotification(
          "Соперник найден! Игра начинается...",
          setNotification
        );
        break;

      case "game_start":
        console.log("Game starting:", message.data);
        if (message.data) {
          gameModel.clearGameData();
          gameModel.saveGameData(message.data);
        }
        setIsWaiting(false);
        router.push("/task");
        break;

      case "game_won":
        console.log("Game won on main page:", message.data);
        if (window.location.pathname === "/") {
          const winnerIdFromMessage = message.data?.winner_id;
          const currentPlayerId = parseInt(playerID?.toString() || "0");

          if (
            winnerIdFromMessage &&
            parseInt(winnerIdFromMessage.toString()) === currentPlayerId
          ) {
            console.log(
              "Показываем победу на главной для игрока",
              currentPlayerId
            );
            showNotification("Поздравляем! Вы выиграли!", setNotification);
          }
        }
        break;

      case "game_lost":
        console.log("Game lost on main page:", message.data);
        if (window.location.pathname === "/") {
          const loserIdFromMessage = message.data?.loser_id;
          const currentPlayerId = parseInt(playerID?.toString() || "0");

          if (
            loserIdFromMessage &&
            parseInt(loserIdFromMessage.toString()) === currentPlayerId
          ) {
            console.log(
              "Показываем поражение на главной для игрока",
              currentPlayerId
            );
            showNotification(
              "Вы проиграли. Попробуйте снова!",
              setNotification
            );
          }
        }
        break;

      case "game_end":
        console.log("Game ended:", message.data);
        if (message.data?.reason === "draw") {
          gameModel.saveGameResult({
            state: "timeup",
            reason: "Игра закончилась вничью",
          });
          showNotification("Игра закончилась вничью", setNotification);
        } else if (message.data?.reason === "timeout") {
          gameModel.saveGameResult({
            state: "timeup",
            reason: "Время вышло!",
          });
          showNotification("Время вышло!", setNotification);
        }
        break;

      case "error":
        console.error("Server error:", message.data);
        showNotification(
          message.data?.error || "Произошла ошибка",
          setNotification
        );
        setIsWaiting(false);
        break;

      default:
        console.log("Unhandled message type:", message.type);
    }
  };

  const cancelSearch = () => {
    setIsWaiting(false);
    setQueueSize(undefined);

    if (playerID && websocketRef.current) {
      sendMessage("leave_queue");
    }
  };

  const handleReady = async () => {
    if (!playerID) {
      showNotification("Инициализация игрока...", setNotification);
      return;
    }

    setIsWaiting(true);

    try {
      const ws = connectWebSocket(playerID);

      const setupWebSocketListeners = (websocket: WebSocket) => {
        websocket.onmessage = (event) => {
          try {
            const message: WebSocketMessage = JSON.parse(event.data);
            console.log("Received message:", message);
            handleWebSocketMessage(message);
          } catch (error) {
            console.error("Error parsing message:", error);
          }
        };

        websocket.onopen = () => {
          console.log("WebSocket connected on main page");
          sendMessage("join_queue", { player_id: playerID });
        };

        websocket.onclose = () => {
          console.log("WebSocket disconnected on main page");
          setIsWaiting(false);
        };

        websocket.onerror = (error) => {
          console.error("WebSocket error on main page:", error);
          setIsWaiting(false);
        };
      };

      if (ws.readyState === WebSocket.OPEN) {
        setupWebSocketListeners(ws);
      } else {
        ws.addEventListener("open", () => setupWebSocketListeners(ws));
      }
    } catch (error) {
      console.error("Error starting game:", error);
      showNotification("Ошибка подключения", setNotification);
      setIsWaiting(false);
    }
  };

  return (
    <main className="min-h-screen flex flex-col items-center justify-center p-3 lg:p-6 relative animate-fadeIn gpu-optimized">      {isWaiting && (
        <WaitingOverlay onCancel={cancelSearch} queueSize={queueSize} />
      )}

      {notification && (
        <div className="fixed top-4 left-1/2 transform -translate-x-1/2 bg-black/80 text-white px-4 py-2 rounded-lg z-50 animate-slideInFromTop">
          {notification}
        </div>
      )}

      <div className="container max-w-6xl">
        <div className="card overflow-hidden animate-scaleIn will-change-transform">
          <div className="flex flex-col lg:flex-row">
            <div className="lg:w-2/3 relative overflow-hidden">
              <Image
                src="/task.png"
                alt="Task Per Minute"
                width={900}
                height={600}
                className="w-full h-48 sm:h-64 lg:h-full object-cover animate-slideInLeft will-change-transform"
                priority
              />
              <div className="absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-transparent lg:bg-gradient-to-r lg:from-transparent lg:via-transparent lg:to-black/60"></div>
              
              <div className="absolute top-4 right-4 w-12 h-12 bg-white/10 rounded-full animate-bounce hidden lg:block"></div>
              <div className="absolute bottom-6 left-6 w-8 h-8 bg-white/20 rounded-full animate-pulse hidden lg:block"></div>
            </div>
            
            <div className="lg:w-1/3 p-6 lg:p-8 flex flex-col justify-center animate-slideInRight">
              <h1 className="text-2xl lg:text-3xl xl:text-4xl font-bold mb-4 lg:mb-6 animate-glow">
                🚀 Task Per Minute
              </h1>
              
              <h2 className="text-lg lg:text-xl font-semibold mb-4 text-blue-200">
                Сразься в дуэли CTF!
              </h2>
              
              <p className="text-sm lg:text-base text-gray-300 mb-6 lg:mb-8 leading-relaxed">
                Испытай наш новый формат CTF соревнований на скорость! 
                Каждая секунда решает исход битвы.
              </p>
                <div className="animate-on-hover will-change-transform">
                <PlayButton 
                  onClick={handleReady} 
                  disabled={!playerID}
                />
              </div>
              
              <div className="mt-4 lg:mt-6 text-xs text-gray-400">
                {playerID ? (
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 bg-green-400 rounded-full animate-pulse"></div>
                    Игрок готов (ID: {playerID})
                  </div>
                ) : (
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 bg-yellow-400 rounded-full animate-pulse"></div>
                    Инициализация...
                  </div>
                )}
              </div>
            </div>
          </div>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4 lg:gap-6 mt-6 lg:mt-8 animate-fadeIn" style={{animationDelay: '0.3s'}}>
          <div className="card p-4 lg:p-6 animate-on-hover will-change-transform">
            <div className="text-3xl lg:text-4xl mb-3 text-center">⚡</div>
            <h3 className="font-bold text-lg mb-2 text-center">Скоростные дуэли</h3>
            <p className="text-sm text-gray-300 text-center">
              2 игрока, 1 задание, ограниченное время
            </p>
          </div>
          
          <div className="card p-4 lg:p-6 animate-on-hover will-change-transform">
            <div className="text-3xl lg:text-4xl mb-3 text-center">🏆</div>
            <h3 className="font-bold text-lg mb-2 text-center">Победитель</h3>
            <p className="text-sm text-gray-300 text-center">
              Первый решивший задание побеждает
            </p>
          </div>
          
          <div className="card p-4 lg:p-6 animate-on-hover will-change-transform md:col-span-2 lg:col-span-1">
            <div className="text-3xl lg:text-4xl mb-3 text-center">🧩</div>
            <h3 className="font-bold text-lg mb-2 text-center">Web задания</h3>
            <p className="text-sm text-gray-300 text-center">
              Реальные уязвимости и хакерские вызовы
            </p>
          </div>
        </div>

        <div className="card p-6 lg:p-8 mt-6 lg:mt-8 animate-fadeIn" style={{animationDelay: '0.6s'}}>
          <h3 className="text-xl lg:text-2xl font-bold mb-4 text-center">Правила игры</h3>
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 lg:gap-8 text-sm lg:text-base text-gray-300">
            <div>
              <p className="mb-3">
                В испытании принимают участие 2 человека. Даётся случайный таск 
                категории Web на ограниченное количество времени.
              </p>
              <p>
                Время меняется в зависимости от сложности задания.
              </p>
            </div>
            <div>
              <p className="mb-3">
                Побеждает тот, кто первый выполнит задание в заданное время.
              </p>
              <p>
                Если оба участника не смогут выполнить таск — оба являются 
                проигравшими и игра завершается.
              </p>
            </div>
          </div>
        </div>
      </div>
    </main>
  );
}
