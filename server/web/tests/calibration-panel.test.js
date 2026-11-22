import test from 'node:test';
import assert from 'node:assert/strict';
import {
  MIN_COMPLETE_MARKS,
  canCompute,
  describeJump,
  describeLomo,
  describeMark,
  describeSaved,
  describeState,
  errorMessage,
  formatCoordinates,
  requestJson,
} from '../js/ui/calibration-panel.js';
import { describeCalibration } from '../js/ui/hud.js';

const session = (overrides = {}) => ({
  state: 'coletando',
  goal: 15,
  complete_marks: 0,
  active: null,
  marks: [
    { label: 'M1', x: -2.5, z: -1.25, status: 'pendente', samples: { standing: 0, sitting: 0, lying: 0 } },
    { label: 'M2', x: 0, z: -1.25, status: 'completo', samples: { standing: 15, sitting: 3, lying: 0 } },
  ],
  ...overrides,
});

test('mark title shows label and coordinates in meters', () => {
  const info = describeMark(session().marks[0], 15);
  assert.equal(info.title, 'M1 (-2.50, -1.25) m');
  assert.equal(info.status, 'pendente');
});

test('mark counters show every posture against the goal', () => {
  const info = describeMark(session().marks[1], 15);
  assert.match(info.counts, /em pé 15\/15/);
  assert.match(info.counts, /sentado 3\/15/);
  assert.match(info.counts, /deitado 0\/15/);
});

test('state description shows progress of the active mark', () => {
  const state = session({ state: 'coletando_marco', active: { label: 'M2', posture: 'sitting', buffered: 4 } });
  assert.equal(describeState(state), 'coletando M2, sentado: 3/15');
});

test('state sem_csi warns and keeps the count', () => {
  const state = session({ state: 'sem_csi', active: { label: 'M2', posture: 'standing', buffered: 0 } });
  assert.match(describeState(state), /sem CSI/);
  assert.match(describeState(state), /15\/15/);
});

test('missing session reads as empty', () => {
  assert.equal(describeState(null), 'sem sessão');
  assert.equal(describeState({ state: 'vazia' }), 'sem sessão');
});

test('compute needs the minimum of complete marks', () => {
  assert.equal(canCompute(session({ complete_marks: MIN_COMPLETE_MARKS - 1 })), false);
  assert.equal(canCompute(session({ complete_marks: MIN_COMPLETE_MARKS })), true);
  assert.equal(canCompute(null), false);
});

test('lomo and saved descriptions', () => {
  assert.equal(
    describeLomo({ mean_m: 0.512, p90_m: 0.9, marks: 5, windows: 75 }),
    'erro médio 0.51 m · p90 0.90 m · 5 marcos · 75 janelas',
  );
  assert.equal(describeLomo(null), 'sem erro medido');
  assert.equal(
    describeSaved({ id: 'cal-1', marks: 5, lomo_mean_m: 0.4, status: 'ativa' }),
    'cal-1 · 5 marcos · erro 0.40 m · ativa',
  );
});

test('hud calibration line', () => {
  assert.equal(describeCalibration(null), 'nenhuma');
  assert.equal(describeCalibration({ id: 'cal-1', lomo_mean_m: 0.61 }), 'cal-1 · lomo 0.61 m');
  assert.equal(describeCalibration({ id: 'cal-1' }), 'cal-1');
});

test('coordinates format', () => {
  assert.equal(formatCoordinates({ x: 1, z: -2 }), '(1.00, -2.00) m');
});

test('requestJson returns the payload on success', async () => {
  const fetchImpl = async () => ({ ok: true, status: 200, json: async () => ({ a: 1 }) });
  assert.deepEqual(await requestJson('GET', '/x', undefined, fetchImpl), { a: 1 });
});

test('requestJson surfaces the server error code and message', async () => {
  const fetchImpl = async () => ({
    ok: false,
    status: 409,
    json: async () => ({ error: { code: 'CALIBRATION_TOO_FEW_MARKS', message: '3 marcos completos; minimo 4' } }),
  });
  await assert.rejects(requestJson('POST', '/x', {}, fetchImpl), (error) => {
    assert.equal(errorMessage(error), '3 marcos completos; minimo 4');
    return true;
  });
});

test('requestJson does not hide an unreadable success body', async () => {
  const fetchImpl = async () => ({
    ok: true,
    status: 200,
    json: async () => {
      throw new Error('bad json');
    },
  });
  await assert.rejects(requestJson('GET', '/x', undefined, fetchImpl), /ilegível/);
});

test('requestJson sends the JSON body', async () => {
  let seen = null;
  const fetchImpl = async (path, options) => {
    seen = { path, options };
    return { ok: true, status: 200, json: async () => ({}) };
  };
  await requestJson('POST', '/y', { posture: 'lying' }, fetchImpl);
  assert.equal(seen.options.method, 'POST');
  assert.equal(seen.options.body, '{"posture":"lying"}');
  assert.equal(seen.options.headers['Content-Type'], 'application/json');
});

test('jump warning shows distance in meters only above the limit', () => {
  assert.equal(describeJump({ position_jump_warning: true, position_jump_m: 1.234 }), 'Atenção: a posição mudou 1.23 m com a nova calibração');
  assert.equal(describeJump({ position_jump_warning: false, position_jump_m: 0.4 }), '');
  assert.equal(describeJump({ position_jump_warning: false, position_jump_m: null }), '');
  assert.equal(describeJump(null), '');
});
