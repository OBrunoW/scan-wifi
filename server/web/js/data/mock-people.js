export const MOCK_MIN_PEOPLE = 1;
export const MOCK_MAX_PEOPLE = 4;

const ROOM = {
  width: 6,
  depth: 5,
  esp_x: -2.5,
  esp_z: -2,
  esp2_x: 2.5,
  esp2_z: -2,
  tx_x: 0,
  tx_z: 2.2,
  link_m: 5,
  esp1_ok: true,
  esp2_ok: true,
  tx_ok: true,
  map_geometry: 'triangle',
  map_scale: 'measured',
};

const POSE_CYCLE = ['standing', 'sitting', 'lying', 'standing', 'arm_l_up', 'arm_r_up', 'leg_l_up', 'leg_r_up'];
const STILL_POSES = ['standing', 'sitting', 'lying', 'standing'];
const PROFILES = ['adult', 'adult', 'child', 'pet'];
const POSE_PERIOD_MS = 6000;

export function clampMockCount(value) {
  const parsed = Math.round(Number(value));
  if (!Number.isFinite(parsed)) return 2;
  return Math.min(MOCK_MAX_PEOPLE, Math.max(MOCK_MIN_PEOPLE, parsed));
}

function stillPerson(index, tMs) {
  const jitter = 0.02 * Math.sin(tMs / 700 + index);
  return {
    id: index + 1,
    x: -1.5 + index * 1.0 + jitter,
    y: -0.5 + (index % 2) * 1.2 + jitter,
    pose: STILL_POSES[index % STILL_POSES.length],
    profile: PROFILES[index % PROFILES.length],
    motion: 0.05,
    confidence: 0.9,
  };
}

function movingPerson(index, tMs) {
  const phase = index * 1.7;
  const seconds = tMs / 1000;
  const poseStep = Math.floor(tMs / POSE_PERIOD_MS) + index * 2;
  return {
    id: index + 1,
    x: Math.sin(seconds * 0.35 + phase) * 2.2,
    y: Math.cos(seconds * 0.27 + phase * 1.3) * 1.8,
    pose: POSE_CYCLE[poseStep % POSE_CYCLE.length],
    profile: PROFILES[index % PROFILES.length],
    motion: 0.3 + 0.5 * Math.abs(Math.sin(seconds * 0.5 + phase)),
    confidence: 0.55 + 0.45 * Math.abs(Math.sin(seconds * 0.3 + phase)),
  };
}

export function createMock({ count = 2, still = false } = {}) {
  const total = clampMockCount(count);
  const build = still ? stillPerson : movingPerson;
  return {
    count: total,
    frame(tMs) {
      const people = [];
      for (let index = 0; index < total; index++) people.push(build(index, tMs));
      return {
        presence: true,
        count: total,
        people,
        room: { ...ROOM },
        udp_nodes: 2,
        udp_live: ['ESP_NODE_01', 'ESP_NODE_02'],
        csi_rate: 100,
        ml_source: 'mock',
        ts: tMs,
      };
    },
  };
}

export function parseMockQuery(search) {
  const params = new URLSearchParams(search);
  const flag = params.get('mock');
  if (flag === null || flag === '0' || flag === 'false') return null;
  return { still: flag === 'still', count: clampMockCount(params.get('n') ?? 2) };
}
