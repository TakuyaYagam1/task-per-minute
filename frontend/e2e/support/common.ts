export const jsonHeaders = {
  'Content-Type': 'application/json',
};

export const nowISO = () => new Date().toISOString();

export const inSecondsISO = (seconds: number) => new Date(Date.now() + seconds * 1000).toISOString();
