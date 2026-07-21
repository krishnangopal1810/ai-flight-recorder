import "@testing-library/jest-dom/vitest";

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: key => values.get(key) ?? null,
    key: index => Array.from(values.keys())[index] ?? null,
    removeItem: key => { values.delete(key); },
    setItem: (key, value) => { values.set(key, String(value)); },
  };
}

Object.defineProperty(window, "localStorage", {value: memoryStorage(), configurable: true});
Object.defineProperty(window, "sessionStorage", {value: memoryStorage(), configurable: true});
