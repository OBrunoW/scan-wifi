export const COLORMAPS = {
  viridis: ['#440154', '#3b528b', '#21918c', '#5ec962', '#fde725'],
  plasma: ['#0d0887', '#7e03a8', '#cc4778', '#f89540', '#f0f921'],
  magma: ['#000004', '#51127c', '#b73779', '#fc8961', '#fcfdbf'],
  turbo: ['#30123b', '#3e9bfe', '#a2fc3c', '#fb8022', '#7a0403'],
};

export const COLORMAP_STOP_COUNT = 5;

function hexToRgb(hex) {
  const value = parseInt(hex.slice(1), 16);
  return [(value >> 16) & 255, (value >> 8) & 255, value & 255].map((channel) => channel / 255);
}

export function colormapStops(name) {
  return (COLORMAPS[name] || COLORMAPS.viridis).map(hexToRgb);
}

export function sampleColormap(name, t) {
  const stops = colormapStops(name);
  const scaled = Math.min(1, Math.max(0, t)) * (stops.length - 1);
  const index = Math.min(stops.length - 2, Math.floor(scaled));
  const k = scaled - index;
  return stops[index].map((channel, i) => channel + (stops[index + 1][i] - channel) * k);
}
