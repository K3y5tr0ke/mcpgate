import '@testing-library/jest-dom'

// jsdom in this setup does not provide a working localStorage; supply a simple
// in-memory implementation so components/hooks that persist state can be tested.
if (typeof globalThis.localStorage === 'undefined' || typeof globalThis.localStorage.setItem !== 'function') {
  const store = new Map<string, string>()
  const mock: Storage = {
    get length() {
      return store.size
    },
    clear: () => store.clear(),
    getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    removeItem: (k: string) => store.delete(k),
    setItem: (k: string, v: string) => {
      store.set(k, String(v))
    },
  }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: mock })
}

// jsdom does not implement matchMedia; default to "light" unless a test overrides it.
if (typeof globalThis.matchMedia !== 'function') {
  Object.defineProperty(globalThis, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

// jsdom does not implement the <dialog> showModal/close methods used by Modal.
// Provide minimal polyfills so components that open a <dialog> can be tested.
if (typeof HTMLDialogElement !== 'undefined') {
  if (!HTMLDialogElement.prototype.showModal) {
    HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
      this.open = true
    }
  }
  if (!HTMLDialogElement.prototype.close) {
    HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
      this.open = false
      this.dispatchEvent(new Event('close'))
    }
  }
}
