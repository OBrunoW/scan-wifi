import * as THREE from 'three';
import { buildBody } from './body-shape.js';

export const GRID_CELL_METERS = 0.05;
const MIN_CELLS_AROUND = 4;
const MIN_CELLS_ALONG = 2;

function gridCellsFor(mesh) {
  const geometry = mesh.geometry;
  geometry.computeBoundingBox();
  const size = new THREE.Vector3();
  geometry.boundingBox.getSize(size);
  const extentX = size.x * mesh.scale.x;
  const extentY = size.y * mesh.scale.y;
  const extentZ = size.z * mesh.scale.z;
  const circumference = Math.PI * (extentX + extentZ) * 0.5;
  const length = geometry.type === 'SphereGeometry' ? Math.PI * extentY * 0.5 : extentY;
  return [
    Math.max(MIN_CELLS_AROUND, Math.round(circumference / GRID_CELL_METERS)),
    Math.max(MIN_CELLS_ALONG, Math.round(length / GRID_CELL_METERS)),
  ];
}

function attachGrid(mesh) {
  const geometry = mesh.geometry;
  const count = geometry.attributes.position.count;
  const [cellsAround, cellsAlong] = gridCellsFor(mesh);
  const grid = new Float32Array(count * 2);
  for (let i = 0; i < count; i++) {
    grid[i * 2] = cellsAround;
    grid[i * 2 + 1] = cellsAlong;
  }
  geometry.setAttribute('aGrid', new THREE.BufferAttribute(grid, 2));
}

export function buildWireBody(pose, heat, material, renderOrder = 0) {
  const body = buildBody(pose, heat);
  const outlines = [];
  const surfaces = [];
  body.traverse((object) => {
    if (!object.isMesh) return;
    if (object.material.wireframe) outlines.push(object);
    else surfaces.push(object);
  });
  for (const outline of outlines) {
    outline.parent.remove(outline);
    outline.geometry.dispose();
    outline.material.dispose();
  }
  for (const surface of surfaces) {
    surface.material.dispose();
    surface.material = material;
    surface.renderOrder = renderOrder;
    attachGrid(surface);
  }
  body.userData.material = material;
  return body;
}
