import test from 'node:test';
import assert from 'node:assert/strict';
import { PeopleStore, FADE_MS, LEAVE_HOLD_MS, POSE_CONFIRM_MS, PRESENCE } from '../js/data/people-store.js';
import { OneEuroFilter } from '../js/data/one-euro.js';

const room = { width: 6, depth: 5, tx_x: 0, tx_z: 0 };
const person = (overrides = {}) => ({ id: 1, x: 1, y: 2, pose: 'standing', profile: 'adult', motion: 0.1, confidence: 0.8, ...overrides });
const newStore = () => new PeopleStore({ getRoom: () => room });

test('new person enters with alpha zero and reaches 1 after 300 ms', () => {
  const store = newStore();
  store.ingest([person()], 0);
  const record = store.list[0];
  assert.equal(record.state, PRESENCE.ENTERING);
  assert.equal(record.alpha, 0);
  store.advance(FADE_MS / 2, 150);
  assert.ok(Math.abs(record.alpha - 0.5) < 1e-9);
  store.advance(FADE_MS / 2, 300);
  assert.equal(record.alpha, 1);
  assert.equal(record.state, PRESENCE.PRESENT);
});

test('missing person is held, then fades out in 300 ms and is removed', () => {
  const store = newStore();
  store.ingest([person()], 0);
  store.advance(FADE_MS, 300);
  store.ingest([person({ id: 2 })], 1000);
  assert.equal(store.records.get(1).state, PRESENCE.PRESENT);
  store.ingest([person({ id: 2 })], 1000 + LEAVE_HOLD_MS + 1);
  assert.equal(store.records.get(1).state, PRESENCE.LEAVING);
  store.advance(FADE_MS / 2, 2000);
  assert.ok(store.records.has(1));
  store.advance(FADE_MS / 2, 2150);
  assert.equal(store.records.has(1), false);
  assert.equal(store.list.length, 1);
});

test('person returning while leaving resumes from current alpha', () => {
  const store = newStore();
  store.ingest([person()], 0);
  store.advance(FADE_MS, 300);
  store.clear();
  store.advance(FADE_MS / 2, 450);
  const record = store.records.get(1);
  const alphaBefore = record.alpha;
  store.ingest([person({ x: 3 })], 460);
  assert.equal(record.state, PRESENCE.ENTERING);
  assert.equal(record.alpha, alphaBefore);
  assert.equal(record.current.x, 300);
});

test('dead zone keeps the target for small jumps when still', () => {
  const store = newStore();
  store.ingest([person()], 0);
  store.ingest([person({ x: 1.1 })], 100);
  assert.equal(store.records.get(1).target.x, 100);
  store.ingest([person({ x: 1.2 })], 200);
  assert.equal(store.records.get(1).target.x, 120);
});

test('pose change is confirmed only after 400 ms and bumps poseVersion', () => {
  const store = newStore();
  store.ingest([person()], 0);
  const record = store.records.get(1);
  store.ingest([person({ pose: 'sitting' })], 100);
  assert.equal(record.poseVersion, 0);
  store.ingest([person({ pose: 'sitting' })], 100 + POSE_CONFIRM_MS);
  assert.equal(record.poseVersion, 1);
  assert.equal(record.target.pose, 'sitting');
});

test('missing confidence defaults to 1 and values are clamped', () => {
  const store = newStore();
  store.ingest([{ id: 1, x: 0, y: 0 }, { id: 2, x: 0, y: 0, confidence: 4 }], 0);
  assert.equal(store.records.get(1).confidence, 1);
  assert.equal(store.records.get(2).confidence, 1);
});

test('person that stays still varies less than 2 cm per frame for 60 s', () => {
  const store = newStore();
  store.ingest([person()], 0);
  const record = store.list[0];
  let previous = null;
  let worst = 0;
  for (let ms = 16; ms <= 60000; ms += 16) {
    if (ms % 200 < 16) store.ingest([person({ x: 1 + 0.02 * Math.sin(ms / 700), y: 2 + 0.02 * Math.sin(ms / 700) })], ms);
    store.advance(16, ms);
    if (previous) worst = Math.max(worst, Math.hypot(record.smooth.x - previous.x, record.smooth.y - previous.y));
    previous = { x: record.smooth.x, y: record.smooth.y };
  }
  assert.ok(worst < 2, `worst frame delta ${worst}`);
});

test('one euro filter converges to a constant input and smooths noise', () => {
  const filter = new OneEuroFilter({ minCutoff: 1, beta: 0 });
  filter.filter(0, 0);
  let value = 0;
  for (let ms = 16; ms < 3000; ms += 16) value = filter.filter(10, ms);
  assert.ok(Math.abs(value - 10) < 0.1);
  const stepped = new OneEuroFilter({ minCutoff: 1, beta: 0 });
  stepped.filter(0, 0);
  assert.ok(stepped.filter(10, 16) < 2);
});
