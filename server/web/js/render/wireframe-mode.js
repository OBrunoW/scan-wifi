import * as THREE from 'three';
import { FigureMode } from './figure-mode.js';
import { buildWireBody } from './body-geometry.js';
import { COLORMAP_STOP_COUNT, colormapStops } from './colormaps.js';
import { wireframeVertexShader } from './shaders/wireframe.vert.js';
import { wireframeFragmentShader } from './shaders/wireframe.frag.js';

const DEPTH_RANGE_CM = 800;

function createStopVectors() {
  const vectors = [];
  for (let i = 0; i < COLORMAP_STOP_COUNT; i++) vectors.push(new THREE.Vector3());
  return vectors;
}

export function confidenceFactor(confidence, threshold) {
  if (threshold <= 0) return 1;
  return Math.min(1, Math.max(0, confidence / threshold));
}

export class WireframeMode extends FigureMode {
  constructor({ camera, controls, ...options } = {}) {
    super({
      id: 'wireframe',
      buildBody: (pose, heat, mode) => mode.buildWireFigure(pose, heat),
      skipLeaving: false,
      ...options,
    });
    this.camera = camera;
    this.controls = controls;
    this.opacity = 0.9;
    this.density = 1;
    this.thickness = 1.2;
    this.colormap = 'viridis';
    this.backOpacity = 0.25;
    this.flicker = 0;
    this.confidenceThreshold = 0.3;
    this.appliedColormap = null;
    this.stops = createStopVectors();
    this.uniforms = {
      uTime: { value: 0 },
      uDensity: { value: 1 },
      uThickness: { value: 1.2 },
      uBackOpacity: { value: 0.25 },
      uFlicker: { value: 0 },
      uDepthRef: { value: 800 },
      uDepthRange: { value: DEPTH_RANGE_CM },
      uStops: { value: this.stops },
    };
  }

  buildWireFigure(pose, heat) {
    const material = new THREE.ShaderMaterial({
      uniforms: { ...this.uniforms, uAlpha: { value: 0 } },
      vertexShader: wireframeVertexShader,
      fragmentShader: wireframeFragmentShader,
      transparent: true,
      depthWrite: false,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      toneMapped: false,
    });
    return buildWireBody(pose, heat, material, 0);
  }

  applyColormap() {
    if (this.appliedColormap === this.colormap) return;
    const stops = colormapStops(this.colormap);
    stops.forEach(([r, g, b], i) => {
      const color = new THREE.Color().setRGB(r, g, b, THREE.SRGBColorSpace);
      this.stops[i].set(color.r, color.g, color.b);
    });
    this.appliedColormap = this.colormap;
  }

  positionOf(record) {
    return record.smooth;
  }

  opacityScaleOf(record) {
    return record.alpha;
  }

  afterFigureUpdate(figure, record) {
    const material = figure.userData.parts.body?.userData.material;
    if (!material) return;
    material.uniforms.uAlpha.value =
      record.alpha * confidenceFactor(record.confidence, this.confidenceThreshold) * this.opacity;
  }

  update(people, dtMs) {
    const u = this.uniforms;
    u.uTime.value += dtMs / 1000;
    u.uDensity.value = this.density;
    u.uThickness.value = this.thickness;
    u.uBackOpacity.value = this.backOpacity;
    u.uFlicker.value = this.flicker;
    if (this.camera && this.controls) {
      u.uDepthRef.value = this.camera.position.distanceTo(this.controls.target);
    }
    this.applyColormap();
    super.update(people, dtMs);
  }
}

