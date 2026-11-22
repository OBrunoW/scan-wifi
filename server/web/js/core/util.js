export const M_TO_CM = 100;

export const m2cm = (v) => Number(v) * M_TO_CM;

export const num = (v, fallback = 0) => (Number.isFinite(Number(v)) ? Number(v) : fallback);

export const lerp = (a, b, t) => a + (b - a) * t;

export const clamp = (value, min, max) => Math.min(max, Math.max(min, value));

export function distHeat(room, xM, zM) {
  const txX = num(room.tx_x);
  const txZ = num(room.tx_z);
  const d = Math.hypot(num(xM) - txX, num(zM) - txZ);
  const maxD = Math.max(num(room.width, 6), num(room.depth, 6), 4) * 0.55;
  return Math.min(1, Math.max(0, 1 - d / maxD));
}
