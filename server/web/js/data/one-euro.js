const TWO_PI = Math.PI * 2;

function smoothingFactor(cutoffHz, dtSeconds) {
  const tau = 1 / (TWO_PI * cutoffHz);
  return 1 / (1 + tau / dtSeconds);
}

export class OneEuroFilter {
  constructor({ minCutoff = 1.0, beta = 0.02, derivativeCutoff = 1.0 } = {}) {
    this.minCutoff = minCutoff;
    this.beta = beta;
    this.derivativeCutoff = derivativeCutoff;
    this.reset();
  }

  setParams({ minCutoff, beta }) {
    if (Number.isFinite(minCutoff) && minCutoff > 0) this.minCutoff = minCutoff;
    if (Number.isFinite(beta) && beta >= 0) this.beta = beta;
  }

  reset(value = null) {
    this.value = value;
    this.derivative = 0;
    this.lastMs = null;
  }

  filter(value, nowMs) {
    if (this.value === null || this.lastMs === null) {
      this.value = value;
      this.lastMs = nowMs;
      return value;
    }
    const dtSeconds = (nowMs - this.lastMs) / 1000;
    if (dtSeconds <= 0) return this.value;
    const rawDerivative = (value - this.value) / dtSeconds;
    const derivativeAlpha = smoothingFactor(this.derivativeCutoff, dtSeconds);
    this.derivative += derivativeAlpha * (rawDerivative - this.derivative);
    const cutoff = this.minCutoff + this.beta * Math.abs(this.derivative);
    const alpha = smoothingFactor(cutoff, dtSeconds);
    this.value += alpha * (value - this.value);
    this.lastMs = nowMs;
    return this.value;
  }
}
