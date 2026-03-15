export type ToastType = 'info' | 'success' | 'warn' | 'alert';

export type Toast = {
  id: number;
  message: string;
  type: ToastType;
  timeout: ReturnType<typeof setTimeout> | null;
  duration: number;
  createdAt: number;
};

let nextId = 0;

export const storeToast = $state<Toast[]>([]);

export const addToast = (message: string, type: ToastType = 'info', duration = 4000) => {
  const id = nextId++;

  // Limit to 5 visible toasts — remove oldest if needed
  if (storeToast.length >= 5) {
    const oldest = storeToast[0];
    if (oldest?.timeout) clearTimeout(oldest.timeout);
    storeToast.splice(0, 1);
  }

  storeToast.push({
    id,
    message,
    type,
    duration,
    createdAt: Date.now(),
    timeout: duration > 0 ? setTimeout(() => removeToast(id), duration) : null,
  });
};

export const removeToast = (id: number) => {
  const index = storeToast.findIndex(t => t.id === id);
  if (index !== -1) {
    if (storeToast[index]?.timeout) {
      clearTimeout(storeToast[index].timeout!);
    }
    storeToast.splice(index, 1);
  }
};
