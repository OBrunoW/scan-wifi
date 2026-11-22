export const RENDER_MODE_METHODS = ['init', 'update', 'dispose'];

export function assertRenderMode(mode) {
  for (const method of RENDER_MODE_METHODS) {
    if (typeof mode[method] !== 'function') {
      throw new Error(`render mode is missing ${method}`);
    }
  }
  return mode;
}
