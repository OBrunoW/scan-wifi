import * as THREE from 'three';
import { M_TO_CM, m2cm, lerp } from '../core/util.js';
import { disposeObject, faintMat, lineMat, makeLabel } from '../render/three-helpers.js';

export const ROOM_HEIGHT_CM = 270;
const DEFAULT_ROOM_SIDE_M = 6;
const SHELL_COLOR = 0x64748b;
const WALL_COLOR = 0x9ca3af;
const SHELL_LINE_OPACITY = 0.25;
const WALL_FACE_OPACITY = 0.08;
const WALL_LINE_OPACITY = 0.18;

function makePointMarker(color, label, stale = false) {
  const g = new THREE.Group();
  const core = new THREE.Mesh(
    new THREE.SphereGeometry(8, 16, 12),
    new THREE.MeshBasicMaterial({ color, toneMapped: false, transparent: stale, opacity: stale ? 0.35 : 1 })
  );
  const ring = new THREE.Mesh(
    new THREE.RingGeometry(12, 16, 24),
    faintMat(color, 0.55)
  );
  ring.rotation.x = -Math.PI / 2;
  ring.position.y = 1;
  core.position.y = 8;
  const tag = makeLabel(stale ? `${label} sem sinal` : label, stale);
  tag.position.set(0, 32, 0);
  g.add(core, ring, tag);
  return g;
}
function addRoomTraces(r) {
  const g = new THREE.Group();
  const spanM = Math.max(40, Number(r.width) || 0, Number(r.depth) || 0) * 1.8;
  const span = spanM * M_TO_CM;
  const cell = 10;
  const divisions = Math.max(40, Math.round(span / cell));
  const grid = new THREE.GridHelper(span, divisions, 0x1e293b, 0x0b1220);
  const mats = Array.isArray(grid.material) ? grid.material : [grid.material];
  mats.forEach((m) => {
    m.transparent = true;
    m.opacity = 0.22;
    m.toneMapped = false;
    m.depthWrite = false;
  });
  grid.position.y = 0.001;
  g.add(grid);
  return g;
}

function buildShell(r) {
  const width = (Number(r.width) > 0 ? Number(r.width) : DEFAULT_ROOM_SIDE_M) * M_TO_CM;
  const depth = (Number(r.depth) > 0 ? Number(r.depth) : DEFAULT_ROOM_SIDE_M) * M_TO_CM;
  const box = new THREE.LineSegments(
    new THREE.EdgesGeometry(new THREE.BoxGeometry(width, ROOM_HEIGHT_CM, depth)),
    lineMat(SHELL_COLOR, SHELL_LINE_OPACITY)
  );
  box.position.y = ROOM_HEIGHT_CM / 2;
  return { box, width, depth };
}

function buildWall(width) {
  const group = new THREE.Group();
  const geometry = new THREE.PlaneGeometry(width, ROOM_HEIGHT_CM);
  group.add(new THREE.Mesh(geometry, faintMat(WALL_COLOR, WALL_FACE_OPACITY)));
  group.add(new THREE.LineSegments(new THREE.EdgesGeometry(geometry), lineMat(WALL_COLOR, WALL_LINE_OPACITY)));
  group.position.y = ROOM_HEIGHT_CM / 2;
  return group;
}

export function createRoomScene(parent) {
  const markersGroup = new THREE.Group();
  parent.add(markersGroup);
  const shellGroup = new THREE.Group();
  parent.add(shellGroup);

  const markerTargets = {
    esp1: { x: 0, z: 0, on: false, stale: false },
    esp2: { x: 0, z: 0, on: false, stale: false },
    tx: { x: 0, z: 0, on: false, stale: false },
  };
  const markerMeshes = { esp1: null, esp2: null, tx: null };
  const state = {
    udpLive: [],
    floorGroup: null,
    floorVisible: true,
    shellVisible: false,
    wallOn: true,
    wallPosition: 0.5,
    wall: null,
    depth: 0,
  };

  function applyShellVisibility() {
    shellGroup.visible = state.shellVisible;
    if (state.wall) state.wall.visible = state.wallOn;
  }

  function placeWall() {
    if (state.wall) state.wall.position.z = (state.wallPosition - 0.5) * state.depth;
  }

  function rebuildShell(r) {
    while (shellGroup.children.length) disposeObject(shellGroup.children.pop());
    const { box, width, depth } = buildShell(r);
    state.depth = depth;
    state.wall = buildWall(width);
    shellGroup.add(box, state.wall);
    placeWall();
    applyShellVisibility();
  }

  function clearMarker(key) {
    const m = markerMeshes[key];
    if (!m) return;
    markersGroup.remove(m);
    disposeObject(m);
    markerMeshes[key] = null;
  }

  function ensureMarker(key, color, label, xM, zM, stale = false) {
    const x = m2cm(xM);
    const z = m2cm(zM);
    if (!markerMeshes[key]) {
      const g = makePointMarker(color, label, stale);
      markersGroup.add(g);
      markerMeshes[key] = g;
      g.position.set(x, 0, z);
    }
    markerTargets[key].x = x;
    markerTargets[key].z = z;
    markerTargets[key].on = true;
    markerTargets[key].stale = stale;
  }

  function isNodeStale(r, nodeId) {
    if (!Array.isArray(r.udp_live) && state.udpLive.length === 0) return false;
    const live = Array.isArray(r.udp_live) ? r.udp_live : state.udpLive;
    return !live.includes(nodeId);
  }

  function rebuildMarkers(r) {
    while (markersGroup.children.length) {
      const c = markersGroup.children.pop();
      disposeObject(c);
    }
    markerMeshes.esp1 = null;
    markerMeshes.esp2 = null;
    markerMeshes.tx = null;
    state.floorGroup = addRoomTraces(r);
    state.floorGroup.visible = state.floorVisible;
    markersGroup.add(state.floorGroup);

    markerTargets.esp1.on = false;
    markerTargets.esp2.on = false;
    markerTargets.tx.on = false;

    if (r.esp1_ok && Number.isFinite(Number(r.esp_x)) && Number.isFinite(Number(r.esp_z))) {
      ensureMarker('esp1', 0x22d3ee, 'ESP_NODE_01', Number(r.esp_x), Number(r.esp_z), isNodeStale(r, 'ESP_NODE_01'));
    }
    if (r.esp2_ok && Number.isFinite(Number(r.esp2_x)) && Number.isFinite(Number(r.esp2_z))) {
      ensureMarker('esp2', 0x38bdf8, 'ESP_NODE_02', Number(r.esp2_x), Number(r.esp2_z), isNodeStale(r, 'ESP_NODE_02'));
    }
    if (r.tx_ok && Number.isFinite(Number(r.tx_x)) && Number.isFinite(Number(r.tx_z))) {
      ensureMarker('tx', 0xf59e0b, 'emissor', Number(r.tx_x), Number(r.tx_z));
    }

    for (const key of ['esp1', 'esp2', 'tx']) {
      if (!markerTargets[key].on) clearMarker(key);
    }
  }

  function syncMarkerTargets(r) {
    const visChanged =
      !!r.esp1_ok !== markerTargets.esp1.on
      || !!r.esp2_ok !== markerTargets.esp2.on
      || !!r.tx_ok !== markerTargets.tx.on
      || (r.esp1_ok && isNodeStale(r, 'ESP_NODE_01') !== markerTargets.esp1.stale)
      || (r.esp2_ok && isNodeStale(r, 'ESP_NODE_02') !== markerTargets.esp2.stale)
      || (!!markerMeshes.esp1) !== markerTargets.esp1.on
      || (!!markerMeshes.esp2) !== markerTargets.esp2.on
      || (!!markerMeshes.tx) !== markerTargets.tx.on;

    if (visChanged) {
      rebuildMarkers(r);
      return;
    }
    if (markerTargets.esp1.on) {
      markerTargets.esp1.x = m2cm(r.esp_x);
      markerTargets.esp1.z = m2cm(r.esp_z);
    }
    if (markerTargets.esp2.on) {
      markerTargets.esp2.x = m2cm(r.esp2_x);
      markerTargets.esp2.z = m2cm(r.esp2_z);
    }
    if (markerTargets.tx.on) {
      markerTargets.tx.x = m2cm(r.tx_x);
      markerTargets.tx.z = m2cm(r.tx_z);
    }
  }

  function step() {
    for (const key of ['esp1', 'esp2', 'tx']) {
      const mesh = markerMeshes[key];
      const tgt = markerTargets[key];
      if (!mesh || !tgt.on) continue;
      mesh.position.x = lerp(mesh.position.x, tgt.x, 0.08);
      mesh.position.z = lerp(mesh.position.z, tgt.z, 0.08);
    }
  }

  return {
    markersGroup,
    rebuild(r) {
      rebuildMarkers(r);
      rebuildShell(r);
    },
    syncTargets: syncMarkerTargets,
    step,
    setUdpLive(live) {
      state.udpLive = live;
    },
    setFloorVisible(visible) {
      state.floorVisible = visible;
      if (state.floorGroup) state.floorGroup.visible = visible;
    },
    setShellVisible(visible) {
      state.shellVisible = visible;
      applyShellVisibility();
    },
    setWall({ on, position }) {
      state.wallOn = on;
      state.wallPosition = position;
      placeWall();
      applyShellVisibility();
    },
  };
}
