import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';
import { M_TO_CM, num } from './util.js';

export function createScene(wrap) {
  const scene = new THREE.Scene();
  scene.background = new THREE.Color(0x000000);
  scene.fog = new THREE.FogExp2(0x000000, 0.00045);

  const camera = new THREE.PerspectiveCamera(42, 1, 1, 20000);
  camera.position.set(520, 680, 720);

  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false });
  renderer.setClearColor(0x000000, 1);
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  wrap.style.background = '#000000';
  wrap.appendChild(renderer.domElement);

  const labelRenderer = new CSS2DRenderer();
  labelRenderer.domElement.style.position = 'absolute';
  labelRenderer.domElement.style.inset = '0';
  labelRenderer.domElement.style.pointerEvents = 'none';
  wrap.appendChild(labelRenderer.domElement);

  const controls = new OrbitControls(camera, renderer.domElement);
  controls.enableDamping = true;
  controls.target.set(0, 40, 0);
  controls.maxPolarAngle = Math.PI * 0.49;

  const peopleGroup = new THREE.Group();
  scene.add(peopleGroup);

  const resizeListeners = [];

  function resize() {
    const w = wrap.clientWidth;
    const h = wrap.clientHeight;
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    renderer.setSize(w, h, false);
    labelRenderer.setSize(w, h);
    for (const listener of resizeListeners) listener(w, h);
  }
  window.addEventListener('resize', resize);

  function roomSpan(r) {
    const w = num(r.width) > 0 ? num(r.width) : 6;
    const d = num(r.depth) > 0 ? num(r.depth) : 6;
    return Math.max(w, d) * M_TO_CM;
  }

  function fitCamera(r) {
    const span = roomSpan(r);
    camera.position.set(span * 0.75, Math.max(400, span * 0.55), span * 0.95);
    controls.target.set(0, 40, 0);
    controls.update();
  }

  const presets = {
    front: (span) => ({ position: [0, 170, span * 1.1], target: [0, 90, 0] }),
    through_wall: (span) => ({ position: [0, 170, -span * 1.0], target: [0, 90, 0] }),
    top: (span) => ({ position: [0, span * 1.6, span * 0.01], target: [0, 0, 0] }),
  };

  function applyPreset(name, r) {
    const build = presets[name];
    if (!build) return;
    const { position, target } = build(roomSpan(r));
    camera.position.set(...position);
    controls.target.set(...target);
    controls.update();
  }

  return {
    scene,
    camera,
    renderer,
    labelRenderer,
    controls,
    peopleGroup,
    resize,
    fitCamera,
    applyPreset,
    onResize: (listener) => resizeListeners.push(listener),
    size: () => ({ width: wrap.clientWidth, height: wrap.clientHeight }),
  };
}
