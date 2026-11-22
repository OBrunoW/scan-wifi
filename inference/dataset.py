"""Dataset rotulado CSI (.npy + meta JSON com postura e coordenadas)."""
from __future__ import annotations

import json
import random
from pathlib import Path

import numpy as np
import torch
from torch.utils.data import Dataset

from model import POSE_TO_IDX, POSE_ALIASES, PROFILE_TO_IDX, PROFILE_ALIASES

ROOT = Path(__file__).resolve().parent
DATA_DIR = ROOT / "data"
WINDOW = 32
SUBCARRIERS = 32


def _normalize_pose(raw: str) -> str | None:
    key = (raw or "").strip().lower().replace("-", "_")
    return POSE_ALIASES.get(key) or (key if key in POSE_TO_IDX else None)


def _window_pair(
    amp: np.ndarray,
    phase: np.ndarray,
    window: int,
    subcarriers: int,
) -> np.ndarray | None:
    t = min(amp.shape[0], phase.shape[0])
    s = min(amp.shape[1], phase.shape[1], subcarriers)
    if t < 4:
        return None
    amp = amp[:t, :s] / 255.0
    phase = phase[:t, :s] / 180.0
    if t >= window:
        start = random.randint(0, t - window)
        amp_w = amp[start : start + window]
        phase_w = phase[start : start + window]
    else:
        pad = window - t
        amp_w = np.pad(amp, ((pad, 0), (0, 0)), mode="edge")
        phase_w = np.pad(phase, ((pad, 0), (0, 0)), mode="edge")
    if s < subcarriers:
        amp_w = np.pad(amp_w, ((0, 0), (0, subcarriers - s)))
        phase_w = np.pad(phase_w, ((0, 0), (0, subcarriers - s)))
    return np.stack([amp_w, phase_w], axis=0).astype(np.float32)


def _coords_from_meta(meta: dict, pose: str, amp_w: np.ndarray) -> np.ndarray:
    """Preferência: XYZ rotulado; senão proxy a partir da energia CSI."""
    ref = meta.get("xyz") or meta.get("coord") or {}
    if isinstance(ref, dict) and all(k in ref for k in ("x", "y", "z")):
        return np.array([float(ref["x"]), float(ref["y"]), float(ref["z"])], dtype=np.float32)
    if isinstance(ref, (list, tuple)) and len(ref) >= 3:
        return np.array([float(ref[0]), float(ref[1]), float(ref[2])], dtype=np.float32)

    energy = float(np.mean(np.abs(amp_w - amp_w.mean())))
    depth = float(np.clip(energy * 8, -1, 1))
    s = amp_w.shape[1]
    lateral = float(np.clip((amp_w[:, : s // 2].mean() - amp_w[:, s // 2 :].mean()) * 4, -1, 1))
    height = {"standing": 0.9, "sitting": 0.35, "lying": 0.2}.get(pose, 0.0)
    return np.array([lateral, height, depth], dtype=np.float32)


def augment_samples(
    samples: list[tuple[np.ndarray, int, int, np.ndarray]],
    copies: int = 3,
) -> list[tuple[np.ndarray, int, int, np.ndarray]]:
    out = list(samples)
    for x, y, pidx, coord in samples:
        for _ in range(max(0, copies)):
            noise = np.random.normal(0, 0.02, size=x.shape).astype(np.float32)
            out.append(((x + noise).astype(np.float32), y, pidx, coord.copy()))
    return out


def load_samples(
    data_dir: Path = DATA_DIR,
    window: int = WINDOW,
    subcarriers: int = SUBCARRIERS,
) -> list[tuple[np.ndarray, int, int, np.ndarray]]:
    out: list[tuple[np.ndarray, int, int, np.ndarray]] = []
    data_dir.mkdir(parents=True, exist_ok=True)
    for meta_path in sorted(data_dir.glob("*.json")):
        try:
            meta = json.loads(meta_path.read_text())
        except Exception:
            continue
        pose = _normalize_pose(str(meta.get("posture", "")))
        if pose is None:
            continue
        stem = meta_path.with_suffix("")
        amp_p = Path(f"{stem}_amp.npy")
        phase_p = Path(f"{stem}_phase.npy")
        if not amp_p.exists() or not phase_p.exists():
            continue
        amp = np.load(amp_p).astype(np.float32)
        phase = np.load(phase_p).astype(np.float32)
        x = _window_pair(amp, phase, window, subcarriers)
        if x is None:
            continue
        y = POSE_TO_IDX[pose]
        raw_prof = str(meta.get("profile", "adult"))
        prof_key = PROFILE_ALIASES.get(raw_prof.lower(), "adult")
        pidx = PROFILE_TO_IDX.get(prof_key, 0)
        coord = _coords_from_meta(meta, pose, x[0])
        out.append((x, y, pidx, coord))
    return out


class CSIDataset(Dataset):
    def __init__(self, samples: list[tuple[np.ndarray, int, int, np.ndarray]] | None = None):
        self.samples = samples if samples is not None else load_samples()

    def __len__(self) -> int:
        return len(self.samples)

    def __getitem__(self, idx: int):
        x, y, pidx, coord = self.samples[idx]
        return (
            torch.from_numpy(x),
            torch.tensor(y, dtype=torch.long),
            torch.tensor(pidx, dtype=torch.long),
            torch.from_numpy(coord),
        )
