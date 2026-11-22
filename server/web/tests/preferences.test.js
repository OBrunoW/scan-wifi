import test from 'node:test';
import assert from 'node:assert/strict';
import { createPreferences, SCHEMA } from '../js/data/preferences.js';

function memoryStorage(initial = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (key) => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: (key) => data.delete(key),
    data,
  };
}

const silenceWarnings = (fn) => {
  const original = console.warn;
  console.warn = () => {};
  try {
    return fn();
  } finally {
    console.warn = original;
  }
};

test('defaults are returned when storage is empty', () => {
  const values = createPreferences(() => memoryStorage()).load();
  assert.equal(values.colormap, 'viridis');
  assert.equal(values.back_face_opacity, 0.25);
  assert.equal(values.flicker, 0);
});

test('invalid values fall back and numbers are clamped', () => {
  const storage = memoryStorage({
    'scanwifi.colormap': 'bogus',
    'scanwifi.wire_density': '99',
    'scanwifi.bloom': 'abc',
    'scanwifi.wall': 'maybe',
  });
  const values = createPreferences(() => storage).load();
  assert.equal(values.colormap, 'viridis');
  assert.equal(values.wire_density, SCHEMA.wire_density.max);
  assert.equal(values.bloom, SCHEMA.bloom.fallback);
  assert.equal(values.wall, true);
});

test('values saved are loaded back', () => {
  const storage = memoryStorage();
  const preferences = createPreferences(() => storage);
  preferences.save('wall', false);
  preferences.save('colormap', 'turbo');
  const values = preferences.load();
  assert.equal(values.wall, false);
  assert.equal(values.colormap, 'turbo');
});

test('blocked storage does not throw and yields defaults', () => {
  const blocked = () => {
    throw new Error('SecurityError');
  };
  const preferences = createPreferences(blocked);
  silenceWarnings(() => {
    const values = preferences.load();
    assert.equal(values.colormap, 'viridis');
    preferences.save('colormap', 'turbo');
  });
});

test('throwing setItem does not throw', () => {
  const storage = { getItem: () => null, setItem: () => { throw new Error('quota'); }, removeItem: () => {} };
  silenceWarnings(() => createPreferences(() => storage).save('bloom', 1));
});
