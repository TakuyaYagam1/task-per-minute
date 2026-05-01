// Генерирует случайное имя пользователя
export const generateUsername = (): string => {
  return `Player_${Math.random().toString(36).substr(2, 9)}`;
};

// Форматирует время в секундах в формат MM:SS
export const formatTime = (seconds: number): string => {
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(remainingSeconds).padStart(2, "0")}`;
};

// Показывает уведомление пользователю на определенное время
export const showNotification = (
  message: string,
  setNotification: (msg: string | null) => void,
  duration: number = 3000
): void => {
  setNotification(message);
  setTimeout(() => setNotification(null), duration);
};
