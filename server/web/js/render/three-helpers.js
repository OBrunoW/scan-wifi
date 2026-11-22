import * as THREE from 'three';
import { CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js';

export function faintMat(color, opacity) {
  return new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity,
    side: THREE.DoubleSide,
    depthWrite: false,
    toneMapped: false,
  });
}

export function lineMat(color, opacity) {
  return new THREE.LineBasicMaterial({
    color,
    transparent: true,
    opacity,
    depthWrite: false,
    toneMapped: false,
  });
}

export function makeLabel(text, faint = false) {
  const el = document.createElement('div');
  el.textContent = text;
  el.style.color = faint ? '#64748b' : '#94a3b8';
  el.style.fontSize = faint ? '9px' : '10px';
  el.style.fontFamily = 'ui-monospace, monospace';
  el.style.padding = '1px 5px';
  el.style.background = faint ? 'rgba(0,0,0,0.35)' : 'rgba(0,0,0,0.65)';
  el.style.opacity = faint ? '0.75' : '1';
  el.style.whiteSpace = 'nowrap';
  return new CSS2DObject(el);
}
export function disposeObject(obj) {
  obj.traverse((c) => {
    if (c.element) c.element.remove();
    c.geometry?.dispose?.();
    if (c.material) {
      if (Array.isArray(c.material)) c.material.forEach((m) => m.dispose?.());
      else c.material.dispose?.();
    }
  });
}

const _up = new THREE.Vector3(0, 1, 0);
const _dir = new THREE.Vector3();

export function placeBetween(mesh, ax, ay, az, bx, by, bz) {
  mesh.position.set((ax + bx) * 0.5, (ay + by) * 0.5, (az + bz) * 0.5);
  _dir.set(bx - ax, by - ay, bz - az);
  const len = _dir.length();
  if (len > 1e-6) {
    _dir.multiplyScalar(1 / len);
    mesh.quaternion.setFromUnitVectors(_up, _dir);
  }
}
