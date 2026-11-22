const PREFIX = 'scanwifi.';

export const COLORMAP_NAMES = ['viridis', 'plasma', 'magma', 'turbo'];
export const CAMERA_PRESETS = ['front', 'through_wall', 'top'];

export const SCHEMA = {
  wire_opacity: { type: 'number', min: 0, max: 1, fallback: 0.9 },
  wire_density: { type: 'number', min: 0.3, max: 3, fallback: 1 },
  wire_thickness: { type: 'number', min: 0.5, max: 4, fallback: 1.2 },
  colormap: { type: 'enum', values: COLORMAP_NAMES, fallback: 'viridis' },
  bloom: { type: 'number', min: 0, max: 2, fallback: 0.6 },
  flicker: { type: 'number', min: 0, max: 1, fallback: 0 },
  back_face_opacity: { type: 'number', min: 0, max: 1, fallback: 0.25 },
  confidence_threshold: { type: 'number', min: 0, max: 1, fallback: 0.3 },
  wall: { type: 'boolean', fallback: true },
  wall_position: { type: 'number', min: 0, max: 1, fallback: 0.5 },
  camera_preset: { type: 'enum', values: ['', ...CAMERA_PRESETS], fallback: '' },
};

export function parseValue(rule, raw) {
  if (raw === null || raw === undefined) return rule.fallback;
  if (rule.type === 'enum') return rule.values.includes(raw) ? raw : rule.fallback;
  if (rule.type === 'boolean') {
    if (raw === '1' || raw === 'true') return true;
    if (raw === '0' || raw === 'false') return false;
    return rule.fallback;
  }
  const parsed = Number(raw);
  if (raw === '' || !Number.isFinite(parsed)) return rule.fallback;
  return Math.min(rule.max, Math.max(rule.min, parsed));
}

function serializeValue(rule, value) {
  if (rule.type === 'boolean') return value ? '1' : '0';
  return String(value);
}

export function createPreferences(getStorage) {
  function readRaw(key) {
    try {
      return getStorage().getItem(PREFIX + key);
    } catch (err) {
      console.warn('preference not read', key, err);
      return null;
    }
  }

  function writeRaw(key, raw) {
    try {
      getStorage().setItem(PREFIX + key, raw);
    } catch (err) {
      console.warn('preference not saved', key, err);
    }
  }

  function load() {
    const values = {};
    for (const [key, rule] of Object.entries(SCHEMA)) {
      values[key] = parseValue(rule, readRaw(key));
    }
    return values;
  }

  function save(key, value) {
    const rule = SCHEMA[key];
    if (!rule) return;
    writeRaw(key, serializeValue(rule, value));
  }

  return { load, save };
}
