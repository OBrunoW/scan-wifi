import { m2cm, num, lerp, distHeat } from '../core/util.js';
import { OneEuroFilter } from './one-euro.js';

export const STILL_MOTION = 0.25;
export const STILL_DEAD_ZONE_CM = 15;
export const MOVING_DEAD_ZONE_CM = 5;
export const POSE_CONFIRM_MS = 400;
export const LEAVE_HOLD_MS = 1500;
export const FADE_MS = 300;

export const PRESENCE = { ENTERING: 'entering', PRESENT: 'present', LEAVING: 'leaving' };

export class PeopleStore {
  constructor({ getRoom, smoothing = {} } = {}) {
    this.getRoom = getRoom || (() => ({}));
    this.smoothing = { minCutoff: 1.0, beta: 0.02, ...smoothing };
    this.records = new Map();
    this.ordered = [];
  }

  get list() {
    return this.ordered;
  }

  setSmoothing(params) {
    this.smoothing = { ...this.smoothing, ...params };
    for (const record of this.records.values()) {
      record.filterX.setParams(this.smoothing);
      record.filterY.setParams(this.smoothing);
    }
  }

  createRecord(id, person) {
    const x = m2cm(num(person.x));
    const y = m2cm(num(person.y));
    const record = {
      id,
      state: PRESENCE.ENTERING,
      alpha: 0,
      target: { x, y, pose: 'standing', profile: '', motion: 0 },
      current: { x, y },
      smooth: { x, y },
      heat: distHeat(this.getRoom(), num(person.x), num(person.y)),
      confidence: 1,
      poseVersion: 0,
      pendingKey: null,
      pendingSince: 0,
      holdUntil: 0,
      filterX: new OneEuroFilter(this.smoothing),
      filterY: new OneEuroFilter(this.smoothing),
    };
    this.records.set(id, record);
    this.ordered.push(record);
    return record;
  }

  removeRecord(record) {
    this.records.delete(record.id);
    const index = this.ordered.indexOf(record);
    if (index >= 0) this.ordered.splice(index, 1);
  }

  reviveRecord(record, person) {
    const x = m2cm(num(person.x));
    const y = m2cm(num(person.y));
    record.state = PRESENCE.ENTERING;
    record.current.x = x;
    record.current.y = y;
    record.target.x = x;
    record.target.y = y;
    record.smooth.x = x;
    record.smooth.y = y;
    record.filterX.reset();
    record.filterY.reset();
  }

  updateTarget(record, person) {
    const t = record.target;
    const nx = m2cm(num(person.x));
    const ny = m2cm(num(person.y));
    const mot = num(person.motion);
    const jump = Math.hypot(nx - t.x, ny - t.y);
    const deadZone = mot < STILL_MOTION ? STILL_DEAD_ZONE_CM : MOVING_DEAD_ZONE_CM;
    if (jump >= deadZone) {
      t.x = nx;
      t.y = ny;
    }
  }

  updatePose(record, person, nowMs) {
    const pose = person.pose || 'standing';
    const profile = person.profile || '';
    const key = `${pose}|${profile}`;
    const current = `${record.target.pose}|${record.target.profile}`;
    if (key === current) {
      record.pendingKey = null;
      return;
    }
    if (record.pendingKey !== key) {
      record.pendingKey = key;
      record.pendingSince = nowMs;
      return;
    }
    if (nowMs - record.pendingSince >= POSE_CONFIRM_MS) {
      record.target.pose = pose;
      record.target.profile = profile;
      record.pendingKey = null;
      record.poseVersion += 1;
    }
  }

  ingest(people, nowMs) {
    const seen = new Set();
    for (const person of people) {
      const id = Number(person.id);
      if (!Number.isFinite(id)) continue;
      seen.add(id);
      let record = this.records.get(id);
      if (!record) record = this.createRecord(id, person);
      else if (record.state === PRESENCE.LEAVING) this.reviveRecord(record, person);
      record.holdUntil = 0;
      this.updateTarget(record, person);
      record.heat = distHeat(this.getRoom(), num(person.x), num(person.y));
      this.updatePose(record, person, nowMs);
      record.target.motion = num(person.motion);
      record.confidence = person.confidence === undefined || person.confidence === null
        ? 1
        : Math.min(1, Math.max(0, num(person.confidence)));
    }
    for (const record of this.records.values()) {
      if (seen.has(record.id) || record.state === PRESENCE.LEAVING) continue;
      if (!record.holdUntil) record.holdUntil = nowMs + LEAVE_HOLD_MS;
      if (nowMs < record.holdUntil) continue;
      record.holdUntil = 0;
      record.state = PRESENCE.LEAVING;
    }
  }

  clear() {
    for (const record of this.records.values()) {
      record.holdUntil = 0;
      record.state = PRESENCE.LEAVING;
    }
  }

  advance(dtMs, nowMs) {
    const step = dtMs / FADE_MS;
    for (let i = this.ordered.length - 1; i >= 0; i--) {
      const record = this.ordered[i];
      const t = record.target;
      const c = record.current;
      const dist = Math.hypot(t.x - c.x, t.y - c.y);
      const k = dist < 20 ? 0.04 : dist < 60 ? 0.08 : 0.12;
      c.x = lerp(c.x, t.x, k);
      c.y = lerp(c.y, t.y, k);
      record.smooth.x = record.filterX.filter(t.x, nowMs);
      record.smooth.y = record.filterY.filter(t.y, nowMs);
      if (record.state === PRESENCE.LEAVING) {
        record.alpha = Math.max(0, record.alpha - step);
        if (record.alpha === 0) this.removeRecord(record);
        continue;
      }
      record.alpha = Math.min(1, record.alpha + step);
      if (record.alpha === 1) record.state = PRESENCE.PRESENT;
    }
  }
}
