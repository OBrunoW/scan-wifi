import * as THREE from 'three';
import { M_TO_CM } from '../core/util.js';
import { PRESENCE } from '../data/people-store.js';
import { disposeObject, makeLabel } from './three-helpers.js';

const POSE_LABEL = {
  standing: 'standing', sitting: 'sitting', lying: 'lying',
  arm_l_up: 'arm_l', arm_r_up: 'arm_r',
  leg_l_up: 'leg_l', leg_r_up: 'leg_r',
};

const PROFILE_SCALE = { adult: 1.0, child: 0.72, pet: 0.45 };

function applyPose(body, pose) {
  body.rotation.set(0, 0, 0);
  body.position.set(0, 0, 0);
  body.scale.set(1, 1, 1);
  if (pose === 'lying') {
    body.rotation.x = -Math.PI / 2;
    body.position.y = 0.16;
  }
}

function clearParts(root) {
  while (root.children.length) {
    const c = root.children.pop();
    disposeObject(c);
  }
  root.userData.parts = {};
}

export class FigureMode {
  constructor({ id, buildBody, showLabels = true, skipLeaving = true }) {
    this.id = id;
    this.buildBody = buildBody;
    this.showLabels = showLabels;
    this.skipLeaving = skipLeaving;
    this.root = null;
    this.figures = new Map();
    this.seen = new Set();
  }

  init(parent) {
    this.root = new THREE.Group();
    parent.add(this.root);
  }

  setOptions(options) {
    Object.assign(this, options);
  }

  makeFigure(record) {
    const figure = new THREE.Group();
    figure.scale.set(M_TO_CM, M_TO_CM, M_TO_CM);
    figure.userData = { id: record.id, poseVersion: -1, parts: {} };
    this.root.add(figure);
    this.figures.set(record.id, figure);
    return figure;
  }

  rebuildFigure(figure, record) {
    const pose = record.target.pose || 'standing';
    const profile = record.target.profile || '';
    clearParts(figure);

    const body = this.buildBody(pose, record.heat, this);
    applyPose(body, pose);
    const s = PROFILE_SCALE[profile] || 1;
    body.scale.multiplyScalar(s);
    if (pose === 'lying') body.position.y *= s;
    figure.add(body);

    const parts = { body };
    if (this.showLabels) {
      const tagY = (pose === 'lying' ? 0.35 : pose === 'sitting' ? 0.95 : 1.15) * s;
      const tag = makeLabel(profile ? `P${record.id} ${profile}` : `P${record.id}`);
      tag.position.set(0, tagY, 0);
      figure.add(tag);
      parts.tag = tag;
    }
    figure.userData.parts = parts;
    figure.userData.poseVersion = record.poseVersion;
  }

  removeFigure(id) {
    const figure = this.figures.get(id);
    if (!figure) return;
    clearParts(figure);
    this.root.remove(figure);
    this.figures.delete(id);
  }

  positionOf(record) {
    return record.current;
  }

  opacityScaleOf() {
    return 1;
  }

  afterFigureUpdate() {}

  update(people) {
    this.seen.clear();
    for (const record of people) {
      if (this.skipLeaving && record.state === PRESENCE.LEAVING) continue;
      this.seen.add(record.id);
      let figure = this.figures.get(record.id);
      if (!figure) figure = this.makeFigure(record);
      if (figure.userData.poseVersion !== record.poseVersion) this.rebuildFigure(figure, record);
      const position = this.positionOf(record);
      figure.position.x = position.x;
      figure.position.z = position.y;
      const tag = figure.userData.parts.tag;
      if (tag?.element) {
        const label = POSE_LABEL[record.target.pose] || record.target.pose;
        tag.element.textContent =
          `P${record.id} ${label} ${(position.x / M_TO_CM).toFixed(2)},${(position.y / M_TO_CM).toFixed(2)}m`;
        tag.element.style.opacity = String((0.35 + record.heat * 0.65) * this.opacityScaleOf(record));
      }
      this.afterFigureUpdate(figure, record);
    }
    for (const id of this.figures.keys()) {
      if (!this.seen.has(id)) this.removeFigure(id);
    }
  }

  dispose() {
    for (const id of Array.from(this.figures.keys())) this.removeFigure(id);
    if (this.root) this.root.parent?.remove(this.root);
    this.root = null;
  }
}
