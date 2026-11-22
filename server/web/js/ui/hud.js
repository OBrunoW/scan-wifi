export const FPS_WINDOW_MS = 5000;
export const STREAM_STALE_MS = 2500;
const HUD_REFRESH_MS = 250;

export class FpsMeter {
  constructor(windowMs = FPS_WINDOW_MS) {
    this.windowMs = windowMs;
    this.stamps = [];
  }

  tick(nowMs) {
    this.stamps.push(nowMs);
    while (this.stamps.length > 1 && nowMs - this.stamps[0] > this.windowMs) this.stamps.shift();
  }

  fps() {
    if (this.stamps.length < 2) return 0;
    const span = this.stamps[this.stamps.length - 1] - this.stamps[0];
    if (span <= 0) return 0;
    return ((this.stamps.length - 1) * 1000) / span;
  }
}

export function describeStream(nowMs, lastFrameMs, intervalMs) {
  if (!lastFrameMs) return 'sem dados';
  if (nowMs - lastFrameMs > STREAM_STALE_MS) return 'parado';
  return `${Math.round(intervalMs)} ms`;
}

export function describeCalibration(active) {
  if (!active) return 'nenhuma';
  if (typeof active.lomo_mean_m !== 'number') return active.id;
  return `${active.id} · lomo ${active.lomo_mean_m.toFixed(2)} m`;
}

export function createHud(element) {
  const meter = new FpsMeter();
  let lastFrameMs = 0;
  let intervalMs = 0;
  let people = 0;
  let renderedAt = 0;
  let calibration = null;

  return {
    setCalibration(active) {
      calibration = active;
    },
    onStreamFrame(nowMs, peopleCount) {
      if (lastFrameMs) intervalMs = nowMs - lastFrameMs;
      lastFrameMs = nowMs;
      people = peopleCount;
    },
    tick(nowMs) {
      meter.tick(performance.now());
      if (nowMs - renderedAt < HUD_REFRESH_MS) return;
      renderedAt = nowMs;
      element.textContent =
        `fps ${meter.fps().toFixed(0)} · pessoas ${people} · stream ${describeStream(nowMs, lastFrameMs, intervalMs)} · cal ${describeCalibration(calibration)}`;
    },
  };
}
