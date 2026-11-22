import test from 'node:test';
import assert from 'node:assert/strict';
import { createMock, clampMockCount, parseMockQuery } from '../js/data/mock-people.js';
import { COLORMAPS, colormapStops, sampleColormap } from '../js/render/colormaps.js';

const close = (actual, expected) => actual.forEach((v, i) => assert.ok(Math.abs(v - expected[i]) < 1e-9));

const VALID_POSES = ['standing', 'sitting', 'lying', 'arm_l_up', 'arm_r_up', 'leg_l_up', 'leg_r_up'];

test('mock count is clamped between 1 and 4', () => {
  assert.equal(clampMockCount(0), 1);
  assert.equal(clampMockCount(9), 4);
  assert.equal(clampMockCount('3'), 3);
  assert.equal(clampMockCount('x'), 2);
});

test('mock frames have valid people for every count', () => {
  for (let count = 1; count <= 4; count++) {
    for (const still of [false, true]) {
      const mock = createMock({ count, still });
      for (let t = 0; t < 60000; t += 1500) {
        const frame = mock.frame(t);
        assert.equal(frame.people.length, count);
        assert.equal(frame.count, count);
        assert.ok(frame.room.width > 0 && frame.room.depth > 0);
        for (const person of frame.people) {
          assert.ok(VALID_POSES.includes(person.pose));
          assert.ok(['adult', 'child', 'pet'].includes(person.profile));
          assert.ok(person.confidence >= 0 && person.confidence <= 1);
          assert.ok(Math.abs(person.x) <= frame.room.width / 2);
          assert.ok(Math.abs(person.y) <= frame.room.depth / 2);
        }
      }
    }
  }
});

test('still mock keeps pose and moves less than the dead zone', () => {
  const mock = createMock({ count: 2, still: true });
  const first = mock.frame(0).people[0];
  const later = mock.frame(30000).people[0];
  assert.equal(first.pose, later.pose);
  assert.ok(Math.hypot(first.x - later.x, first.y - later.y) < 0.15);
});

test('mock query parsing', () => {
  assert.equal(parseMockQuery(''), null);
  assert.equal(parseMockQuery('?mock=0'), null);
  assert.deepEqual(parseMockQuery('?mock=1&n=4'), { still: false, count: 4 });
  assert.deepEqual(parseMockQuery('?mock=still&n=9'), { still: true, count: 4 });
});

test('colormaps expose the documented endpoints', () => {
  assert.deepEqual(COLORMAPS.viridis, ['#440154', '#3b528b', '#21918c', '#5ec962', '#fde725']);
  for (const name of Object.keys(COLORMAPS)) {
    assert.equal(colormapStops(name).length, 5);
    close(sampleColormap(name, 0), colormapStops(name)[0]);
    close(sampleColormap(name, 1), colormapStops(name)[4]);
  }
  const mid = sampleColormap('viridis', 0.5);
  close(mid, colormapStops('viridis')[2]);
  assert.deepEqual(sampleColormap('viridis', -3), colormapStops('viridis')[0]);
});
