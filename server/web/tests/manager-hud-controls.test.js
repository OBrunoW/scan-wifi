import test from 'node:test';
import assert from 'node:assert/strict';
import { RenderModeManager } from '../js/render/render-mode-manager.js';
import { FpsMeter, describeStream } from '../js/ui/hud.js';

function fakeMode() {
  return {
    counters: { init: 0, update: 0, dispose: 0 },
    options: {},
    init() { this.counters.init += 1; },
    update() { this.counters.update += 1; },
    dispose() { this.counters.dispose += 1; },
    setOptions(options) { Object.assign(this.options, options); },
  };
}

function newManager() {
  const created = [];
  const manager = new RenderModeManager({
    parent: {},
    factory: () => {
      const mode = fakeMode();
      created.push(mode);
      return mode;
    },
  });
  return { manager, created };
}

test('update before start does nothing', () => {
  const { manager, created } = newManager();
  manager.update([], 16);
  assert.equal(created.length, 0);
});

test('start creates the mode once and update reaches it', () => {
  const { manager, created } = newManager();
  manager.start();
  manager.start();
  manager.update([], 16);
  manager.update([], 16);
  assert.equal(created.length, 1);
  assert.equal(created[0].counters.init, 1);
  assert.equal(created[0].counters.update, 2);
});

test('options set before start are applied on init and later ones are forwarded', () => {
  const { manager, created } = newManager();
  manager.setOptions({ density: 2 });
  manager.start();
  assert.equal(created[0].options.density, 2);
  manager.setOptions({ density: 3 });
  assert.equal(created[0].options.density, 3);
});

test('dispose releases the mode and stops updates', () => {
  const { manager, created } = newManager();
  manager.start();
  manager.dispose();
  manager.update([], 16);
  assert.equal(created[0].counters.dispose, 1);
  assert.equal(created[0].counters.update, 0);
});

test('factory result without the render mode contract is rejected', () => {
  const manager = new RenderModeManager({ parent: {}, factory: () => ({}) });
  assert.throws(() => manager.start());
});

test('fps meter averages over the window and stream description', () => {
  const meter = new FpsMeter(5000);
  for (let ms = 0; ms <= 10000; ms += 20) meter.tick(ms);
  assert.ok(Math.abs(meter.fps() - 50) < 0.5);
  assert.equal(describeStream(1000, 0, 0), 'sem dados');
  assert.equal(describeStream(10000, 1000, 200), 'parado');
  assert.equal(describeStream(1500, 1000, 200), '200 ms');
});
