import { COLORMAP_NAMES, SCHEMA } from '../data/preferences.js';

const CAMERA_OPTIONS = [
  ['', 'padrão'],
  ['front', 'frontal'],
  ['through_wall', 'através da parede'],
  ['top', 'vista de cima'],
];

const SLIDERS = [
  { key: 'wire_opacity', label: 'opacidade', step: 0.05 },
  { key: 'wire_density', label: 'densidade da grade', step: 0.1 },
  { key: 'wire_thickness', label: 'espessura (px)', step: 0.1 },
  { key: 'bloom', label: 'bloom (0 desliga)', step: 0.05 },
  { key: 'back_face_opacity', label: 'faces traseiras', step: 0.05 },
  { key: 'confidence_threshold', label: 'limiar de confiança', step: 0.05 },
  { key: 'flicker', label: 'cintilação', step: 0.05 },
  { key: 'wall_position', label: 'posição da parede', step: 0.05 },
];

const ROW_CLASS = 'flex items-center justify-between gap-3 py-0.5';
const INPUT_CLASS = 'pointer-events-auto bg-black text-slate-200 border border-white/20';

function row(labelText, control) {
  const wrapper = document.createElement('label');
  wrapper.className = ROW_CLASS;
  const label = document.createElement('span');
  label.textContent = labelText;
  wrapper.append(label, control);
  return wrapper;
}

function slider({ key, label, step }, value, onChange) {
  const rule = SCHEMA[key];
  const input = document.createElement('input');
  input.type = 'range';
  input.min = String(rule.min);
  input.max = String(rule.max);
  input.step = String(step);
  input.value = String(value);
  input.className = INPUT_CLASS;
  input.addEventListener('input', () => onChange(key, Number(input.value)));
  return row(label, input);
}

function select(label, key, entries, value, onChange) {
  const input = document.createElement('select');
  input.className = INPUT_CLASS;
  for (const [optionValue, optionLabel] of entries) {
    const option = document.createElement('option');
    option.value = optionValue;
    option.textContent = optionLabel;
    input.appendChild(option);
  }
  input.value = value;
  input.addEventListener('change', () => onChange(key, input.value));
  return row(label, input);
}

function checkbox(label, key, value, onChange) {
  const input = document.createElement('input');
  input.type = 'checkbox';
  input.checked = value;
  input.className = INPUT_CLASS;
  input.addEventListener('change', () => onChange(key, input.checked));
  return row(label, input);
}

export function createSettingsPanel({ container, toggle, values, onChange }) {
  container.className =
    'pointer-events-auto absolute right-3 top-9 z-20 hidden w-64 border border-white/10 bg-black/85 p-2 font-mono text-[11px] text-slate-200';
  const heading = document.createElement('div');
  heading.className = 'mb-1 text-slate-500';
  heading.textContent = 'ajustes do wireframe';
  container.appendChild(heading);
  container.appendChild(select('colormap', 'colormap', COLORMAP_NAMES.map((name) => [name, name]), values.colormap, onChange));
  for (const definition of SLIDERS) {
    container.appendChild(slider(definition, values[definition.key], onChange));
  }
  container.appendChild(checkbox('parede', 'wall', values.wall, onChange));
  container.appendChild(select('câmera', 'camera_preset', CAMERA_OPTIONS, values.camera_preset, onChange));
  toggle.addEventListener('click', () => container.classList.toggle('hidden'));
}
