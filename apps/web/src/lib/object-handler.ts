export function convertJSONToFormData(payload: Record<string, unknown>) {
  const formData = new FormData();
  Object.keys(payload).forEach((key) => {
    formData.append(key, payload[key] as string | Blob);
  });
  return formData;
}

export function findIndexById(array: Array<{ id: string }>, targetId: string) {
  const targetIndex = array?.findIndex((item) => item.id === targetId);
  return targetIndex;
}
