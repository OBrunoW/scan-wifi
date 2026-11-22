export const MIN_COMPLETE_MARKS = 4;
export const POLL_MS = 1000;

export const POSTURE_LABELS = {
  standing: 'em pé',
  sitting: 'sentado',
  lying: 'deitado',
};

const STATE_LABELS = {
  vazia: 'sem sessão',
  coletando: 'escolha um marco e uma postura',
  coletando_marco: 'coletando',
  sem_csi: 'sem CSI: verifique os ESPs e o roteador',
  marco_completo: 'marco completo',
};

const MARK_STATUS_LABELS = {
  pendente: 'pendente',
  coletando: 'coletando',
  completo: 'completo',
  pulado: 'pulado',
};

const ROW_CLASS = 'flex flex-wrap items-center gap-1 py-0.5';
const BUTTON_CLASS = 'pointer-events-auto border border-white/20 px-1.5 py-0.5 text-slate-200 hover:bg-white/10 disabled:opacity-40';

export function formatMeters(value) {
  if (typeof value !== 'number' || Number.isNaN(value)) return '—';
  return `${value.toFixed(2)} m`;
}

export function formatCoordinates(mark) {
  return `(${mark.x.toFixed(2)}, ${mark.z.toFixed(2)}) m`;
}

export function describeState(session) {
  if (!session || !session.state) return STATE_LABELS.vazia;
  const base = STATE_LABELS[session.state] || session.state;
  if (session.state !== 'coletando_marco' && session.state !== 'sem_csi') return base;
  const active = session.active;
  if (!active) return base;
  const mark = (session.marks || []).find((item) => item.label === active.label);
  const count = mark ? mark.samples[active.posture] : 0;
  const posture = POSTURE_LABELS[active.posture] || active.posture;
  if (session.state === 'sem_csi') return `${base} (${active.label}, ${posture}, ${count}/${session.goal})`;
  return `${base} ${active.label}, ${posture}: ${count}/${session.goal}`;
}

export function describeMark(mark, goal) {
  const counts = Object.entries(POSTURE_LABELS)
    .map(([posture, label]) => `${label} ${mark.samples[posture] || 0}/${goal}`)
    .join(' · ');
  return {
    title: `${mark.label} ${formatCoordinates(mark)}`,
    status: MARK_STATUS_LABELS[mark.status] || mark.status,
    counts,
  };
}

export function canCompute(session) {
  if (!session || !session.marks) return false;
  return (session.complete_marks || 0) >= MIN_COMPLETE_MARKS;
}

export function describeLomo(lomo) {
  if (!lomo) return 'sem erro medido';
  return `erro médio ${formatMeters(lomo.mean_m)} · p90 ${formatMeters(lomo.p90_m)} · ${lomo.marks} marcos · ${lomo.windows} janelas`;
}

export function describeSaved(item) {
  const marks = `${item.marks} marcos`;
  const error = typeof item.lomo_mean_m === 'number' ? `erro ${formatMeters(item.lomo_mean_m)}` : 'sem erro';
  return `${item.id} · ${marks} · ${error} · ${item.status}`;
}

export function errorMessage(error) {
  if (error && error.body && error.body.error) return error.body.error.message || error.body.error.code;
  return error && error.message ? error.message : 'falha desconhecida';
}

export async function requestJson(method, path, body, fetchImpl = fetch) {
  const options = { method, headers: {} };
  if (body !== undefined) {
    options.headers['Content-Type'] = 'application/json';
    options.body = JSON.stringify(body);
  }
  const response = await fetchImpl(path, options);
  let payload = null;
  try {
    payload = await response.json();
  } catch (parseError) {
    payload = null;
    if (response.ok) throw new Error(`resposta ilegível de ${path}: ${parseError.message}`);
  }
  if (!response.ok) {
    const failure = new Error(`HTTP ${response.status}`);
    failure.body = payload;
    throw failure;
  }
  return payload;
}

export function describeJump(payload) {
  if (!payload || !payload.position_jump_warning) return '';
  return `Atenção: a posição mudou ${payload.position_jump_m.toFixed(2)} m com a nova calibração`;
}

function button(text, onClick, disabled = false) {
  const element = document.createElement('button');
  element.type = 'button';
  element.className = BUTTON_CLASS;
  element.textContent = text;
  element.disabled = disabled;
  element.addEventListener('click', onClick);
  return element;
}

function line(text, className = '') {
  const element = document.createElement('div');
  element.className = className;
  element.textContent = text;
  return element;
}

export function createCalibrationPanel({ container, toggle, request = requestJson, onActiveChange = () => {}, schedule = setInterval, cancel = clearInterval }) {
  container.className =
    'pointer-events-auto absolute right-3 top-9 z-20 hidden max-h-[80vh] w-[calc(100vw-1.5rem)] max-w-sm overflow-x-hidden overflow-y-auto border border-white/10 bg-black/85 p-2 font-mono text-[11px] text-slate-200';

  let timer = null;
  let view = { state: null, saved: [], result: null, notice: '' };
  let renderedKey = '';

  async function call(method, path, body) {
    try {
      return await request(method, path, body);
    } catch (error) {
      view = { ...view, notice: errorMessage(error) };
      render();
      return null;
    }
  }

  async function refresh() {
    const state = await call('GET', '/api/calibration/state');
    if (!state) return;
    const list = await call('GET', '/api/calibration/list');
    view = { ...view, state, saved: list ? list.items : view.saved, notice: view.notice };
    onActiveChange(state.active || null);
    render();
  }

  async function act(method, path, body, keepResult = true) {
    view = { ...view, notice: '' };
    const payload = await call(method, path, body);
    if (payload && !keepResult) view = { ...view, result: null };
    await refresh();
    return payload;
  }

  async function compute() {
    const payload = await act('POST', '/api/calibration/compute', undefined, false);
    if (payload) {
      view = { ...view, result: payload };
      render();
    }
  }

  async function activate(id) {
    const payload = await act('POST', `/api/calibration/${encodeURIComponent(id)}/activate`);
    if (payload) {
      view = { ...view, result: null, notice: describeJump(payload) };
      onActiveChange(payload.active || null);
      await refresh();
    }
  }

  async function discard(id) {
    const payload = await act('POST', `/api/calibration/${encodeURIComponent(id)}/discard`);
    if (payload) view = { ...view, result: null };
    await refresh();
  }

  function renderMark(mark, goal) {
    const info = describeMark(mark, goal);
    const wrapper = document.createElement('div');
    wrapper.className = 'border-t border-white/10 py-1';
    wrapper.appendChild(line(`${info.title} · ${info.status}`, mark.status === 'completo' ? 'text-emerald-300' : ''));
    wrapper.appendChild(line(info.counts, 'text-slate-500'));
    const row = document.createElement('div');
    row.className = ROW_CLASS;
    for (const [posture, label] of Object.entries(POSTURE_LABELS)) {
      const full = (mark.samples[posture] || 0) >= goal;
      row.appendChild(button(label, () => act('POST', `/api/calibration/marks/${encodeURIComponent(mark.label)}/start`, { posture }), full));
    }
    row.appendChild(button('pular', () => act('POST', `/api/calibration/marks/${encodeURIComponent(mark.label)}/skip`)));
    row.appendChild(button('refazer', () => act('DELETE', `/api/calibration/marks/${encodeURIComponent(mark.label)}/samples`)));
    wrapper.appendChild(row);
    return wrapper;
  }

  function renderSession(session) {
    const fragment = document.createDocumentFragment();
    const stateClass = session.state === 'sem_csi' ? 'text-amber-300' : 'text-slate-200';
    fragment.appendChild(line(describeState(session), stateClass));
    for (const mark of session.marks) fragment.appendChild(renderMark(mark, session.goal));
    const row = document.createElement('div');
    row.className = `${ROW_CLASS} border-t border-white/10 pt-1`;
    row.appendChild(button(`calcular (${session.complete_marks}/${MIN_COMPLETE_MARKS} marcos)`, compute, !canCompute(session)));
    fragment.appendChild(row);
    return fragment;
  }

  function renderResult(result) {
    const wrapper = document.createElement('div');
    wrapper.className = 'border-t border-white/10 py-1';
    wrapper.appendChild(line(`calibração ${result.id}`, 'text-emerald-300'));
    wrapper.appendChild(line(describeLomo(result.lomo)));
    const skipped = (result.marks || []).filter((mark) => mark.skipped).map((mark) => mark.label);
    if (skipped.length > 0) wrapper.appendChild(line(`pulados: ${skipped.join(', ')}`, 'text-slate-500'));
    const row = document.createElement('div');
    row.className = ROW_CLASS;
    row.appendChild(button('ativar', () => activate(result.id)));
    row.appendChild(button('descartar', () => discard(result.id)));
    wrapper.appendChild(row);
    return wrapper;
  }

  function renderSaved(items, active) {
    const wrapper = document.createElement('div');
    wrapper.className = 'border-t border-white/10 py-1';
    wrapper.appendChild(line('calibrações salvas', 'text-slate-500'));
    if (items.length === 0) wrapper.appendChild(line('nenhuma'));
    for (const item of items) {
      const isActive = active && active.id === item.id;
      wrapper.appendChild(line(describeSaved(item), isActive ? 'text-emerald-300 break-all' : 'break-all'));
      if (isActive || item.status === 'descartada') continue;
      const row = document.createElement('div');
      row.className = ROW_CLASS;
      row.appendChild(button('ativar', () => activate(item.id)));
      wrapper.appendChild(row);
    }
    return wrapper;
  }

  function render() {
    const key = JSON.stringify(view);
    if (key === renderedKey) return;
    renderedKey = key;
    container.replaceChildren();
    container.appendChild(line('calibração guiada', 'mb-1 text-slate-500'));
    if (view.notice) container.appendChild(line(view.notice, 'mb-1 break-words text-amber-300'));
    const state = view.state;
    if (!state) {
      container.appendChild(line('carregando'));
      return;
    }
    const marksText = state.marks.map((mark) => `${mark.label} ${formatCoordinates(mark)}`).join(' · ');
    container.appendChild(line(`sala ${state.room.width} x ${state.room.depth} m`, 'text-slate-500'));
    container.appendChild(line(marksText, 'break-words text-slate-500'));
    const session = state.session;
    if (!session || session.state === 'vazia') {
      const row = document.createElement('div');
      row.className = `${ROW_CLASS} pt-1`;
      row.appendChild(button('iniciar sessão', () => act('POST', '/api/calibration/session', undefined, false)));
      container.appendChild(row);
    } else {
      container.appendChild(renderSession(session));
    }
    if (view.result) container.appendChild(renderResult(view.result));
    container.appendChild(renderSaved(view.saved, state.active));
  }

  function open() {
    container.classList.remove('hidden');
    refresh();
    timer = schedule(refresh, POLL_MS);
  }

  function close() {
    container.classList.add('hidden');
    if (timer !== null) cancel(timer);
    timer = null;
  }

  toggle.addEventListener('click', () => {
    if (container.classList.contains('hidden')) open();
    else close();
  });

  return { refresh, open, close, render };
}
