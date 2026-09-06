export function isStringEmpty(str?: string | undefined) {
  if (
    typeof str === "undefined" ||
    str === null ||
    str === "" ||
    str.length === 0
  ) {
    return true;
  } else {
    return false;
  }
}

export function isObjectEmpty(obj: object) {
  for (const key in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, key)) return false;
  }
  return true;
}

export function isArrayEmpty(array: Array<unknown>) {
  if (!Array.isArray(array) || !array.length) {
    return true;
  }
  return false;
}
