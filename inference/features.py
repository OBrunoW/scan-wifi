from __future__ import annotations

import numpy as np

from schemas import CSIPacketIn


def packets_to_matrix(packets: list[CSIPacketIn], subcarriers: int | None = None) -> np.ndarray:
    """Retorna array (T, 2, S) com amp e phase normalizados."""
    if not packets:
        raise ValueError("janela CSI vazia")

    lengths = [len(p.amp) for p in packets if p.amp]
    if not lengths:
        raise ValueError("pacotes sem amp")
    s = subcarriers or min(lengths)
    s = max(8, min(s, 64))

    rows = []
    for p in packets:
        amp = np.asarray(p.amp[:s], dtype=np.float32)
        phase = np.asarray((p.phase or [0.0] * s)[:s], dtype=np.float32)
        if amp.shape[0] < s:
            amp = np.pad(amp, (0, s - amp.shape[0]))
            phase = np.pad(phase, (0, s - phase.shape[0]))
        amp = amp / 255.0
        phase = phase / 180.0
        rows.append(np.stack([amp, phase], axis=0))
    return np.stack(rows, axis=0)


def signature_vector(packets: list[CSIPacketIn]) -> np.ndarray:
    """Assinatura média (2*S,) para calibração / KNN."""
    mat = packets_to_matrix(packets)  # (T, 2, S)
    mean = mat.mean(axis=0)  # (2, S)
    std = mat.std(axis=0)
    return np.concatenate([mean.reshape(-1), std.reshape(-1)]).astype(np.float32)


def window_tensor(packets: list[CSIPacketIn], target_t: int = 32) -> np.ndarray:
    """(1, 2, T, S) para a CNN; faz pad/truncate temporal."""
    mat = packets_to_matrix(packets)  # (T, 2, S)
    t, _, s = mat.shape
    if t >= target_t:
        mat = mat[-target_t:]
    else:
        pad = np.repeat(mat[:1], target_t - t, axis=0)
        mat = np.concatenate([pad, mat], axis=0)
    return mat.transpose(1, 0, 2)[None, ...].astype(np.float32)


def zone_coords(packets: list[CSIPacketIn], room_w: float, room_d: float, esp_z: float) -> list[tuple[float, float, float, float]]:
    """Estimativa espacial por zonas. Retorna (x, y, z, energy)."""
    mat = packets_to_matrix(packets)
    amp = mat[:, 0, :]
    baseline = amp[: max(1, amp.shape[0] // 4)].mean(axis=0)
    diff = amp - baseline
    energy = np.sqrt((diff**2).mean(axis=0))
    motion = np.sqrt(np.diff(amp, axis=0, prepend=amp[:1]) ** 2).mean(axis=0)

    zones = 10
    s = energy.shape[0]
    hw = room_w * 0.5
    hd = room_d * 0.5
    near = float(esp_z)
    far = float(hd)
    if far < near:
        near, far = far, near

    scores = []
    for z in range(zones):
        a = z * s // zones
        b = max(a + 1, (z + 1) * s // zones)
        band = energy[a:b]
        mot = motion[a:b]
        e = float(band.mean() + mot.mean() * 1.5)
        peak_i = int(a + np.argmax(band))
        x = -hw + room_w * ((peak_i + 0.5) / max(1, s))
        scores.append((z, e, x))

    active = [t for t in scores if t[1] >= 0.025]
    active.sort(key=lambda t: t[1], reverse=True)
    active = active[:8]
    if not active:
        return []
    emin = min(t[1] for t in active)
    emax = max(t[1] for t in active)
    out: list[tuple[float, float, float, float]] = []
    for z, e, x in active:
        rel = 0.5 if emax <= emin + 1e-9 else (e - emin) / (emax - emin)
        abs_d = float(np.clip(e / 0.12, 0.0, 1.0))
        depth = float(np.clip(0.55 * rel + 0.45 * abs_d, 0.0, 1.0))
        span = (z + 1) / (zones + 1)
        zz = near + depth * (far - near) * 0.92
        zz = 0.75 * zz + 0.25 * (near + span * (far - near))
        out.append((float(x), 0.0, float(zz), float(e)))
    return out


def node_feat_vector(packets: list[CSIPacketIn], subcarriers: int = 32) -> np.ndarray:
    """Media temporal -> (64,) = amp32 + phase32."""
    mat = packets_to_matrix(packets, subcarriers=subcarriers)
    mean = mat.mean(axis=0)
    amp = mean[0]
    phase = mean[1]
    if amp.shape[0] < subcarriers:
        amp = np.pad(amp, (0, subcarriers - amp.shape[0]))
        phase = np.pad(phase, (0, subcarriers - phase.shape[0]))
    return np.concatenate([amp[:subcarriers], phase[:subcarriers]]).astype(np.float32)


def stereo_window_tensor(
    packets_a: list[CSIPacketIn],
    packets_b: list[CSIPacketIn] | None,
    target_t: int = 32,
    subcarriers: int = 32,
) -> np.ndarray:
    """(1, 2, T, 64) — dois nos, 64 feats (amp+phase)."""
    def win(packets: list[CSIPacketIn]) -> np.ndarray:
        mat = packets_to_matrix(packets, subcarriers=subcarriers)
        t = mat.shape[0]
        amp = mat[:, 0, :]
        phase = mat[:, 1, :]
        if amp.shape[1] < subcarriers:
            amp = np.pad(amp, ((0, 0), (0, subcarriers - amp.shape[1])))
            phase = np.pad(phase, ((0, 0), (0, subcarriers - phase.shape[1])))
        feat = np.concatenate([amp[:, :subcarriers], phase[:, :subcarriers]], axis=1)
        if t >= target_t:
            feat = feat[-target_t:]
        else:
            pad = np.repeat(feat[:1], target_t - t, axis=0)
            feat = np.concatenate([pad, feat], axis=0)
        return feat.astype(np.float32)

    a = win(packets_a)
    if packets_b:
        b = win(packets_b)
    else:
        b = a.copy()
    return np.stack([a, b], axis=0)[None, ...]


def band_energy(packets: list[CSIPacketIn]) -> float:
    if not packets:
        return 0.0
    mat = packets_to_matrix(packets)
    amp = mat[:, 0, :]
    baseline = amp[: max(1, amp.shape[0] // 4)].mean(axis=0)
    diff = amp - baseline
    return float(np.sqrt((diff ** 2).mean()))
