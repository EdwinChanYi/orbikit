// jsdom has no ResizeObserver. Arco's menu keeps its observer in a Vue ref,
// which wraps the instance in a Proxy; the polyfill then cannot find itself
// in its WeakMap and throws on observe. A no-op observer keeps components
// that only need layout observation mounting without failing the suite.
class ResizeObserverStub {
  observe() {}

  unobserve() {}

  disconnect() {}
}

globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
