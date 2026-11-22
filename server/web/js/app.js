import { num } from './core/util.js';
import { createScene } from './core/scene.js';
import { createLoop } from './core/loop.js';
import { createRoomScene } from './room/room-scene.js';
import { PeopleStore } from './data/people-store.js';
import { createPreferences } from './data/preferences.js';
import { createMock, parseMockQuery } from './data/mock-people.js';
import { connectWS } from './ws/connection.js';
import { RenderModeManager } from './render/render-mode-manager.js';
import { WireframeMode } from './render/wireframe-mode.js';
import { createPostfx } from './render/postfx.js';
import { createSettingsPanel } from './ui/settings-panel.js';
import { createHud } from './ui/hud.js';
import { createCalibrationPanel, requestJson } from './ui/calibration-panel.js';

const wrap = document.getElementById('canvas-wrap');
const udpStatus = document.getElementById('udp-status');
const csiRate = document.getElementById('csi-rate');
const roomSizeEl = document.getElementById('room-size');
const peopleCountEl = document.getElementById('people-count');
const wsStatus = document.getElementById('ws-status');
const mlStatus = document.getElementById('ml-status');

const WIREFRAME_OPTION_KEYS = {
  wire_opacity: 'opacity',
  wire_density: 'density',
  wire_thickness: 'thickness',
  colormap: 'colormap',
  back_face_opacity: 'backOpacity',
  confidence_threshold: 'confidenceThreshold',
  flicker: 'flicker',
};

const MOCK_INTERVAL_MS = 200;

let room = { width: 0, depth: 0, esp1_ok: false, esp2_ok: false, tx_ok: false };
let udpLive = [];
let mlSeenAt = 0;
let peopleSeenAt = 0;
let postfx = null;

const preferences = createPreferences(() => window.localStorage);
const settings = preferences.load();

const stage = createScene(wrap);
const roomScene = createRoomScene(stage.scene);
const store = new PeopleStore({ getRoom: () => room });
const hud = createHud(document.getElementById('hud'));

const manager = new RenderModeManager({
  parent: stage.peopleGroup,
  factory: () => new WireframeMode({ camera: stage.camera, controls: stage.controls }),
});

function usesBloom() {
  return settings.bloom > 0;
}

function pushWireframeOptions() {
  const options = {};
  for (const [key, optionName] of Object.entries(WIREFRAME_OPTION_KEYS)) options[optionName] = settings[key];
  manager.setOptions(options);
}

function applySetting(key, value) {
  settings[key] = value;
  preferences.save(key, value);
  if (key in WIREFRAME_OPTION_KEYS) pushWireframeOptions();
  if (key === 'wall' || key === 'wall_position') {
    roomScene.setWall({ on: settings.wall, position: settings.wall_position });
  }
  if (key === 'camera_preset') stage.applyPreset(value, room);
}

function ensurePostfx() {
  if (postfx) return postfx;
  const { width, height } = stage.size();
  postfx = createPostfx({ renderer: stage.renderer, scene: stage.scene, camera: stage.camera, width, height });
  return postfx;
}

stage.onResize((width, height) => postfx?.setSize(width, height));

function fitRoom(r) {
  stage.fitCamera(r);
  if (settings.camera_preset) stage.applyPreset(settings.camera_preset, r);
  roomScene.rebuild(r);
}

const SCALE_LABEL = { measured: 'escala: medida', estimated: 'escala: estimada' };
const GEOMETRY_LABEL = { triangle: 'geometria: triângulo', default_angle: 'geometria: ângulo padrão' };

function describeMap(r) {
  const parts = ['auto'];
  if (SCALE_LABEL[r.map_scale]) parts.push(SCALE_LABEL[r.map_scale]);
  if (GEOMETRY_LABEL[r.map_geometry]) parts.push(GEOMETRY_LABEL[r.map_geometry]);
  return parts.join(' · ');
}

function applyRoom(msg) {
  const next = {
    width: num(msg.room.width),
    depth: num(msg.room.depth),
    esp_x: num(msg.room.esp_x),
    esp_z: num(msg.room.esp_z),
    esp2_x: num(msg.room.esp2_x),
    esp2_z: num(msg.room.esp2_z),
    tx_x: num(msg.room.tx_x),
    tx_z: num(msg.room.tx_z),
    link_m: num(msg.room.link_m),
    esp1_ok: !!msg.room.esp1_ok,
    esp2_ok: !!msg.room.esp2_ok,
    tx_ok: !!msg.room.tx_ok,
    map_geometry: String(msg.room.map_geometry || 'none'),
    map_scale: String(msg.room.map_scale || 'none'),
    udp_live: room.udp_live,
  };
  const sizeChanged = next.width !== room.width || next.depth !== room.depth;
  const layoutChanged = next.esp_x !== room.esp_x || next.esp_z !== room.esp_z
    || next.esp2_x !== room.esp2_x || next.esp2_z !== room.esp2_z
    || next.tx_x !== room.tx_x || next.tx_z !== room.tx_z
    || next.esp1_ok !== room.esp1_ok || next.esp2_ok !== room.esp2_ok || next.tx_ok !== room.tx_ok;
  room = { ...room, ...next };
  if (sizeChanged) fitRoom(room);
  else if (layoutChanged) roomScene.syncTargets(room);
  roomSizeEl.textContent = describeMap(room);
}

function applyStatus(msg) {
  const nowMs = Date.now();
  hud.onStreamFrame(nowMs, Array.isArray(msg.people) ? msg.people.length : 0);
  if (msg.room) applyRoom(msg);

  const people = Array.isArray(msg.people) ? msg.people : [];
  if (msg.presence && people.length > 0) {
    peopleSeenAt = nowMs;
    store.ingest(people, nowMs);
  } else if (nowMs - peopleSeenAt > 2500) {
    store.clear();
  }

  const nodes = num(msg.udp_nodes);
  const live = Array.isArray(msg.udp_live) ? msg.udp_live.map(String) : [];
  const liveKey = live.slice().sort().join(',');
  const prevKey = udpLive.slice().sort().join(',');
  if (liveKey !== prevKey) {
    udpLive = live;
    room = { ...room, udp_live: live };
    roomScene.setUdpLive(live);
    roomScene.syncTargets(room);
  }
  if (nodes > 0 && live.length > 0) {
    udpStatus.textContent = `ok:${live.length}`;
    udpStatus.className = 'text-emerald-300';
  } else {
    udpStatus.textContent = 'down';
    udpStatus.className = 'text-amber-300';
  }
  csiRate.textContent = num(msg.csi_rate).toFixed(1);
  peopleCountEl.textContent = String(num(msg.count, people.length));
  if (msg.ml_source) {
    mlSeenAt = nowMs;
  }
  const mlOk = nowMs - mlSeenAt < 2500;
  mlStatus.textContent = mlOk ? 'ok' : 'down';
  mlStatus.className = mlOk
    ? 'border-l border-white/10 px-2 py-2 text-emerald-300'
    : 'border-l border-white/10 px-2 py-2 text-amber-300';
}

function startWebSocket() {
  connectWS({
    onOpen: () => {
      wsStatus.textContent = 'ok';
      wsStatus.className = 'border-l border-white/10 px-2 py-2 text-emerald-300';
    },
    onClose: () => {
      wsStatus.textContent = 'down';
      wsStatus.className = 'border-l border-white/10 px-2 py-2 text-amber-300';
    },
    onMessage: applyStatus,
  });
}

function startMock(options) {
  const mock = createMock(options);
  wsStatus.textContent = 'mock';
  wsStatus.className = 'border-l border-white/10 px-2 py-2 text-amber-300';
  const push = () => applyStatus(mock.frame(Date.now()));
  push();
  setInterval(push, MOCK_INTERVAL_MS);
}

function renderFrame() {
  if (usesBloom()) {
    const effects = ensurePostfx();
    effects.setStrength(settings.bloom);
    effects.render();
  } else {
    stage.renderer.render(stage.scene, stage.camera);
  }
  stage.labelRenderer.render(stage.scene, stage.camera);
}

stage.resize();
fitRoom(room);
pushWireframeOptions();
roomScene.setWall({ on: settings.wall, position: settings.wall_position });
roomScene.setFloorVisible(false);
roomScene.setShellVisible(true);
manager.start();

createSettingsPanel({
  container: document.getElementById('settings-panel'),
  toggle: document.getElementById('settings-toggle'),
  values: settings,
  onChange: applySetting,
});

createCalibrationPanel({
  container: document.getElementById('calibration-panel'),
  toggle: document.getElementById('calibration-toggle'),
  onActiveChange: (active) => hud.setCalibration(active),
});

createLoop({
  onFrame: (dtMs, nowMs) => {
    store.advance(dtMs, nowMs);
    roomScene.step();
    manager.update(store.list, dtMs);
    stage.controls.update();
    renderFrame();
    hud.tick(nowMs);
  },
});

const ACTIVE_CALIBRATION_POLL_MS = 5000;
let activeCalibrationFailing = false;

async function pollActiveCalibration() {
  try {
    const payload = await requestJson('GET', '/api/calibration/active');
    hud.setCalibration(payload.active || null);
    activeCalibrationFailing = false;
  } catch (error) {
    if (!activeCalibrationFailing) console.error('calibração ativa indisponível', error);
    activeCalibrationFailing = true;
  }
}

const mockOptions = parseMockQuery(window.location.search);
if (mockOptions) {
  startMock(mockOptions);
} else {
  startWebSocket();
  pollActiveCalibration();
  setInterval(pollActiveCalibration, ACTIVE_CALIBRATION_POLL_MS);
}
