import * as THREE from 'three';
import { placeBetween } from './three-helpers.js';

function thermalColor(t) {
  t = Math.min(1, Math.max(0, t));
  const c = new THREE.Color();
  const hue = 0.58 - t * 0.50;
  const sat = 0.55 + t * 0.35;
  const light = 0.58 - t * 0.36;
  c.setHSL(hue, sat, light);
  return c;
}

function colorizeGeometry(geometry, heat = 0.5) {
  const g = geometry.index ? geometry.toNonIndexed() : geometry.clone();
  g.computeBoundingBox();
  const pos = g.attributes.position;
  const colors = new Float32Array(pos.count * 3);
  const minY = g.boundingBox.min.y;
  const span = Math.max(1e-6, g.boundingBox.max.y - minY);
  const c = new THREE.Color();
  for (let i = 0; i < pos.count; i++) {
    const local = (pos.getY(i) - minY) / span;
    const ty = Math.min(1, Math.max(0, local * 0.35 + heat * 0.65));
    c.copy(thermalColor(ty));
    colors[i * 3] = c.r;
    colors[i * 3 + 1] = c.g;
    colors[i * 3 + 2] = c.b;
  }
  g.setAttribute('color', new THREE.BufferAttribute(colors, 3));
  return g;
}

function fillMat(opacity = 0.22) {
  return new THREE.MeshBasicMaterial({
    vertexColors: true,
    transparent: true,
    opacity,
    depthWrite: false,
    side: THREE.DoubleSide,
    toneMapped: false,
  });
}

function wireMat(opacity = 0.78) {
  return new THREE.MeshBasicMaterial({
    vertexColors: true,
    wireframe: true,
    transparent: true,
    opacity,
    depthWrite: false,
    toneMapped: false,
  });
}

function part(geo, heat, sx = 1, sy = 1, sz = 1) {
  const colored = colorizeGeometry(geo, heat);
  const group = new THREE.Group();
  const fillOp = 0.10 + heat * 0.34;
  const wireOp = 0.28 + heat * 0.55;
  const fill = new THREE.Mesh(colored, fillMat(fillOp));
  const wire = new THREE.Mesh(colored.clone(), wireMat(wireOp));
  fill.scale.set(sx, sy, sz);
  wire.scale.set(sx, sy, sz);
  group.add(fill, wire);
  return group;
}

function limb(radius, length, heat, segs = 10) {
  const h = Math.max(0.02, length - radius * 2);
  return part(new THREE.CapsuleGeometry(radius, h, 5, segs), heat);
}

function joint(radius, heat, sx = 1, sy = 1, sz = 1) {
  return part(new THREE.SphereGeometry(radius, 14, 12), heat, sx, sy, sz);
}
function torsoShell(w, h, d, heat) {
  const g = new THREE.Group();
  const core = part(new THREE.CapsuleGeometry(w * 0.42, Math.max(0.05, h - w * 0.7), 6, 14), heat, 1, 1, d / (w * 0.84));
  g.add(core);
  return g;
}

export function buildBody(pose = 'standing', baseHeat = 0.55) {
  const heatOf = (bias = 0.5) => Math.min(1, Math.max(0, baseHeat * 0.7 + bias * 0.3));

  const g = new THREE.Group();
  const sit = pose === 'sitting';

  const pelvisY = sit ? 0.50 : 0.90;
  const torsoH = sit ? 0.42 : 0.50;
  const torsoY = sit ? 0.78 : 1.20;
  const chestY = sit ? 1.00 : 1.40;
  const neckY = sit ? 1.14 : 1.54;
  const headY = sit ? 1.28 : 1.68;
  const shoulderY = sit ? 1.04 : 1.44;
  const shoulderX = 0.19;

  const pelvis = joint(0.10, heatOf(0.40), 1.55, 0.72, 1.05);
  pelvis.position.set(0, pelvisY, sit ? 0.03 : 0);
  g.add(pelvis);

  const torso = torsoShell(0.26, torsoH, 0.16, heatOf(0.55));
  torso.position.set(0, torsoY, sit ? 0.03 : 0);
  g.add(torso);

  const chest = joint(0.13, heatOf(0.68), 1.55, 0.70, 0.85);
  chest.position.set(0, chestY, sit ? 0.04 : 0.02);
  g.add(chest);

  const neck = limb(0.038, 0.09, heatOf(0.82), 8);
  neck.position.set(0, neckY, 0.01);
  g.add(neck);

  const head = joint(0.105, heatOf(0.95), 0.92, 1.12, 0.95);
  head.position.set(0, headY, 0.02);
  g.add(head);

  function arm(side) {
    const s = side === 'l' ? -1 : 1;
    const raise = (pose === 'arm_l_up' && side === 'l') || (pose === 'arm_r_up' && side === 'r');
    const group = new THREE.Group();
    group.name = side === 'l' ? 'armL' : 'armR';

    const shoulder = joint(0.048, heatOf(0.72));
    shoulder.position.set(shoulderX * s, shoulderY, 0);
    group.add(shoulder);

    if (sit) {
      const shX = shoulderX * s;
      const shY = shoulderY;
      const elX = 0.24 * s;
      const elY = 0.70;
      const elZ = 0.12;
      const hnX = 0.14 * s;
      const hnY = 0.54;
      const hnZ = 0.34;

      const upperLen = Math.hypot(elX - shX, elY - shY, elZ);
      const lowerLen = Math.hypot(hnX - elX, hnY - elY, hnZ - elZ);

      const upper = limb(0.038, upperLen, heatOf(0.58));
      placeBetween(upper, shX, shY, 0, elX, elY, elZ);
      group.add(upper);

      const elbow = joint(0.034, heatOf(0.48));
      elbow.position.set(elX, elY, elZ);
      group.add(elbow);

      const lower = limb(0.032, lowerLen, heatOf(0.38));
      placeBetween(lower, elX, elY, elZ, hnX, hnY, hnZ);
      group.add(lower);

      const hand = joint(0.032, heatOf(0.28), 0.9, 1.15, 0.65);
      hand.position.set(hnX, hnY, hnZ);
      group.add(hand);
    } else if (raise) {
      const upper = limb(0.038, 0.28, heatOf(0.58));
      upper.position.set(0.20 * s, shoulderY + 0.15, 0.01);
      upper.rotation.z = -1.2 * s;
      group.add(upper);

      const elbow = joint(0.034, heatOf(0.48));
      elbow.position.set(0.18 * s, shoulderY + 0.34, 0.01);
      group.add(elbow);

      const lower = limb(0.032, 0.26, heatOf(0.38));
      lower.position.set(0.18 * s, shoulderY + 0.52, 0.01);
      group.add(lower);

      const hand = joint(0.032, heatOf(0.28), 0.9, 1.15, 0.65);
      hand.position.set(0.18 * s, shoulderY + 0.68, 0.01);
      group.add(hand);
    } else {
      const upper = limb(0.038, 0.30, heatOf(0.58));
      upper.position.set(0.24 * s, 1.28, 0.01);
      upper.rotation.z = 0.08 * s;
      group.add(upper);

      const elbow = joint(0.034, heatOf(0.48));
      elbow.position.set(0.28 * s, 1.10, 0.02);
      group.add(elbow);

      const lower = limb(0.032, 0.27, heatOf(0.38));
      lower.position.set(0.30 * s, 0.93, 0.03);
      lower.rotation.z = 0.05 * s;
      group.add(lower);

      const hand = joint(0.032, heatOf(0.28), 0.9, 1.15, 0.65);
      hand.position.set(0.32 * s, 0.78, 0.04);
      group.add(hand);
    }
    g.add(group);
  }
  arm('l');
  arm('r');

  function leg(side) {
    const s = side === 'l' ? -1 : 1;
    const raise = (pose === 'leg_l_up' && side === 'l') || (pose === 'leg_r_up' && side === 'r');
    const group = new THREE.Group();
    group.name = side === 'l' ? 'legL' : 'legR';
    const hipX = 0.09;

    const hip = joint(0.058, heatOf(0.38));
    hip.position.set(hipX * s, pelvisY - 0.02, 0);
    group.add(hip);

    if (sit) {
      const thigh = limb(0.055, 0.40, heatOf(0.28));
      thigh.position.set(hipX * s, pelvisY - 0.02, 0.22);
      thigh.rotation.x = Math.PI / 2;
      group.add(thigh);

      const knee = joint(0.042, heatOf(0.18));
      knee.position.set(hipX * s, pelvisY - 0.04, 0.44);
      group.add(knee);

      const calf = limb(0.042, 0.38, heatOf(0.10));
      calf.position.set(hipX * s, 0.24, 0.46);
      group.add(calf);

      const foot = limb(0.028, 0.18, heatOf(0.05), 8);
      foot.rotation.z = Math.PI / 2;
      foot.position.set(hipX * s, 0.04, 0.54);
      group.add(foot);
    } else if (raise) {
      const thigh = limb(0.055, 0.40, heatOf(0.28));
      thigh.position.set(hipX * s, 0.78, 0.18);
      thigh.rotation.x = -1.05;
      group.add(thigh);

      const knee = joint(0.042, heatOf(0.18));
      knee.position.set(hipX * s, 0.92, 0.36);
      group.add(knee);

      const calf = limb(0.042, 0.38, heatOf(0.10));
      calf.position.set(hipX * s, 0.76, 0.50);
      calf.rotation.x = 0.55;
      group.add(calf);

      const foot = limb(0.028, 0.18, heatOf(0.05), 8);
      foot.rotation.z = Math.PI / 2;
      foot.position.set(hipX * s, 0.60, 0.58);
      group.add(foot);
    } else {
      const thigh = limb(0.055, 0.42, heatOf(0.28));
      thigh.position.set(hipX * s, 0.66, 0);
      group.add(thigh);

      const knee = joint(0.042, heatOf(0.18));
      knee.position.set(hipX * s, 0.44, 0.01);
      group.add(knee);

      const calf = limb(0.042, 0.40, heatOf(0.10));
      calf.position.set(hipX * s, 0.22, 0.01);
      group.add(calf);

      const ankle = joint(0.032, heatOf(0.08));
      ankle.position.set(hipX * s, 0.05, 0.01);
      group.add(ankle);

      const foot = limb(0.028, 0.20, heatOf(0.05), 8);
      foot.rotation.z = Math.PI / 2;
      foot.position.set(hipX * s, 0.035, 0.08);
      group.add(foot);
    }
    g.add(group);
  }
  leg('l');
  leg('r');

  return g;
}
