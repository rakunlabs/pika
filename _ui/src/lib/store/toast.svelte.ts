type Types = "info" | "warn" | "alert";

type Toast = {
  message: string
  type: Types
  timeout: ReturnType<typeof setTimeout>
  id: number
}

export const storeToast = $state<Array<Toast>>([]);

export const addToast = (message: string, type: Types = "info", timeout = 4000) => {
  const id = storeToast.length == 0 ? 0 : storeToast[storeToast.length - 1].id + 1;
  storeToast.push({
    message,
    type,
    timeout: timeout > 0 ? setTimeout(() => removeToast(id), timeout) : null,
    id: id,
  } as Toast);
};

export const removeToast = (id: number) => {
  const index = storeToast.findIndex(t => t.id === id);
  if (index !== -1) {
    if (storeToast[index]?.timeout) {
      clearTimeout(storeToast[index].timeout);
    }
    storeToast.splice(index, 1);
  }
};
