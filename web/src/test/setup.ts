import '@testing-library/jest-dom/vitest'

// jsdom 没有 ResizeObserver，而 React Flow（概览页的工作流图）在挂载时就会用它。
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// Radix 的 Select 依赖 jsdom 没实现的 Pointer Capture 与 scrollIntoView。
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.setPointerCapture ??= () => {}
Element.prototype.releasePointerCapture ??= () => {}
Element.prototype.scrollIntoView ??= () => {}
