import { assertRenderMode } from './render-mode.js';

export class RenderModeManager {
  constructor({ parent, factory }) {
    this.parent = parent;
    this.factory = factory;
    this.mode = null;
    this.options = {};
  }

  setOptions(options) {
    this.options = { ...this.options, ...options };
    this.mode?.setOptions?.(options);
  }

  start() {
    if (this.mode) return;
    const mode = assertRenderMode(this.factory());
    mode.setOptions?.(this.options);
    mode.init(this.parent);
    this.mode = mode;
  }

  update(people, dtMs) {
    this.mode?.update(people, dtMs);
  }

  dispose() {
    this.mode?.dispose();
    this.mode = null;
  }
}
