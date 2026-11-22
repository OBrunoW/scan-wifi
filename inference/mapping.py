"""Mapeamento espacial: ancoras CSI + triangulacao dual-ESP."""
from __future__ import annotations

import json
import math
import threading
import time
from dataclasses import dataclass, field
from pathlib import Path

import numpy as np

from features import band_energy, signature_vector
from schemas import CSIPacketIn, RoomIn

ROOT = Path(__file__).resolve().parent
MAP_PATH = ROOT / "weights" / "spatial_map.json"


def energy_to_range(energy: float, room: RoomIn) -> float:
    span = max(room.link_m, room.depth * 0.5, 3.0)
    e = max(energy, 1e-6)
    r = span / (1.0 + 8.0 * e)
    return float(np.clip(r, 0.4, span))


def circle_intersect(
    x0: float, z0: float, r0: float,
    x1: float, z1: float, r1: float,
) -> tuple[float, float] | None:
    dx = x1 - x0
    dz = z1 - z0
    d = math.hypot(dx, dz)
    if d < 1e-6 or d > r0 + r1 or d < abs(r0 - r1):
        return None
    a = (r0 * r0 - r1 * r1 + d * d) / (2 * d)
    h2 = r0 * r0 - a * a
    if h2 < 0:
        return None
    h = math.sqrt(h2)
    xm = x0 + a * dx / d
    zm = z0 + a * dz / d
    xs1 = xm + h * (-dz) / d
    zs1 = zm + h * (dx) / d
    xs2 = xm - h * (-dz) / d
    zs2 = zm - h * (dx) / d
    if zs1 >= zs2:
        return xs1, zs1
    return xs2, zs2


def triangulate(
    packets_a: list[CSIPacketIn],
    packets_b: list[CSIPacketIn] | None,
    room: RoomIn,
) -> tuple[float, float, float]:
    e1 = band_energy(packets_a)
    e2 = band_energy(packets_b) if packets_b else e1
    r1 = energy_to_range(e1, room)
    r2 = energy_to_range(e2, room)
    hit = circle_intersect(room.esp_x, room.esp_z, r1, room.esp2_x, room.esp2_z, r2)
    if hit is None:
        t = float(np.clip((e1 + e2) * 4.0, 0.0, 1.0))
        x = 0.5 * (room.esp_x + room.esp2_x)
        z = room.esp_z + t * (room.tx_z - room.esp_z)
        return x, z, 0.25
    x, z = hit
    hw, hd = room.width * 0.48, room.depth * 0.48
    x = float(np.clip(x, -hw, hw))
    z = float(np.clip(z, -hd, hd))
    conf = float(np.clip(0.35 + (e1 + e2) * 2.0, 0.2, 0.95))
    return x, z, conf


def weighted_position(vec: np.ndarray, anchors: list[dict]) -> tuple[float, float, float]:
    dists: list[float] = []
    xs: list[float] = []
    zs: list[float] = []
    for a in anchors:
        sig = np.asarray(a["signature"], dtype=np.float32)
        n = min(sig.shape[0], vec.shape[0])
        d = float(np.linalg.norm(vec[:n] - sig[:n]))
        dists.append(max(d, 1e-6))
        xs.append(float(a["x"]))
        zs.append(float(a["z"]))

    inv = np.array([1.0 / (d ** 2) for d in dists], dtype=np.float64)
    w = inv / inv.sum()
    x = float(np.dot(w, np.asarray(xs)))
    z = float(np.dot(w, np.asarray(zs)))
    nearest = min(dists)
    conf = float(np.clip(np.exp(-nearest * 0.35), 0.05, 0.99))
    return x, z, conf


@dataclass(frozen=True)
class ActiveCalibration:
    id: str
    anchors: list[dict]
    posture_profiles: dict[str, list[float]] = field(default_factory=dict)
    lomo: dict = field(default_factory=dict)
    created_at: int = 0


class SpatialMapper:
    def __init__(self, path: Path = MAP_PATH) -> None:
        self.path = path
        self.anchors: list[dict] = []
        self.guided: ActiveCalibration | None = None
        self.last_vector: np.ndarray | None = None
        self._guided_lock = threading.Lock()
        self._load()

    def set_guided(self, calibration: ActiveCalibration | None) -> None:
        with self._guided_lock:
            self.guided = calibration

    def position_jump(self, candidate: ActiveCalibration) -> float | None:
        vector = self.last_vector
        current = self.effective_anchors()
        if vector is None or not self.ready():
            return None
        before = weighted_position(vector, current)
        after = weighted_position(vector, candidate.anchors)
        return float(np.hypot(after[0] - before[0], after[1] - before[1]))

    def effective_anchors(self) -> list[dict]:
        guided = self.guided
        if guided is not None:
            return guided.anchors
        return self.anchors

    def _load(self) -> None:
        if not self.path.exists():
            self.anchors = []
            return
        try:
            data = json.loads(self.path.read_text())
            self.anchors = list(data.get("anchors", []))
        except Exception:
            self.anchors = []

    def save(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        payload = {"updated_at": int(time.time() * 1000), "anchors": self.anchors}
        self.path.write_text(json.dumps(payload, indent=2))

    def clear(self) -> None:
        self.anchors = []
        if self.path.exists():
            self.path.unlink()

    def ready(self) -> bool:
        return len(self.effective_anchors()) >= 2

    def status(self) -> dict:
        return {
            "ready": self.ready(),
            "anchors": len(self.anchors),
            "points": [
                {"label": a.get("label"), "x": a.get("x"), "y": a.get("y", 0.0), "z": a.get("z")}
                for a in self.anchors
            ],
            "path": str(self.path),
        }

    def calibrate(
        self,
        packets: list[CSIPacketIn],
        x: float,
        z: float,
        y: float = 0.0,
        label: str = "",
    ) -> dict:
        if len(packets) < 5:
            raise ValueError("envie ao menos 5 pacotes CSI para calibrar a ancora")
        vec = signature_vector(packets)
        entry = {
            "label": label or f"a{len(self.anchors)}",
            "x": float(x),
            "y": float(y),
            "z": float(z),
            "signature": vec.astype(float).tolist(),
            "ts": int(time.time() * 1000),
        }
        self.anchors = [a for a in self.anchors if a.get("label") != entry["label"]]
        self.anchors.append(entry)
        self.save()
        return self.status()

    def map_xz(
        self,
        packets: list[CSIPacketIn],
        fallback: tuple[float, float] | None = None,
    ) -> tuple[float, float, float]:
        if not self.ready():
            if fallback is None:
                return 0.0, 0.0, 0.0
            return fallback[0], fallback[1], 0.0

        vec = signature_vector(packets)
        self.last_vector = vec
        x, z, conf = weighted_position(vec, self.effective_anchors())
        if fallback is not None and conf < 0.35:
            x = 0.55 * x + 0.45 * fallback[0]
            z = 0.55 * z + 0.45 * fallback[1]
        return x, z, conf


spatial_mapper = SpatialMapper()
