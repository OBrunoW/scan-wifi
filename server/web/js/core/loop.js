export function createLoop({ onFrame }) {
  let last = null;
  let running = true;

  function frame(nowMs) {
    if (!running) return;
    requestAnimationFrame(frame);
    const dtMs = last === null ? 0 : Math.min(250, nowMs - last);
    last = nowMs;
    onFrame(dtMs, Date.now());
  }

  requestAnimationFrame(frame);

  return {
    stop() {
      running = false;
    },
  };
}
