"""Coleta de janelas CSI rotuladas (postura + XYZ) em .npy."""
from __future__ import annotations

import json
import time
from pathlib import Path

import numpy as np

from features import packets_to_matrix
from schemas import CSIPacketIn, normalize_posture

DATA_DIR = Path(__file__).resolve().parent / "data"


class Collector:
    def __init__(self) -> None:
        self.active: str | None = None
        self.started_at: float | None = None
        self.buffer: list[CSIPacketIn] = []
        self.xyz: dict[str, float] | None = None
        self.profile: str = "adult"
        self.out_dir: Path | None = None
        DATA_DIR.mkdir(parents=True, exist_ok=True)

    def start(
        self,
        posture: str,
        x: float | None = None,
        y: float | None = None,
        z: float | None = None,
        profile: str = "adult",
        out_dir: Path | None = None,
    ) -> dict:
        pose = normalize_posture(posture)
        self.active = pose
        self.out_dir = Path(out_dir) if out_dir is not None else None
        self.profile = (profile or "adult").lower()
        self.started_at = time.time()
        self.buffer = []
        if x is not None and y is not None and z is not None:
            self.xyz = {"x": float(x), "y": float(y), "z": float(z)}
        else:
            self.xyz = None
        return {
            "posture": pose,
            "recording": True,
            "samples": 0,
            "xyz": self.xyz,
            "profile": self.profile,
        }

    def push(self, packets: list[CSIPacketIn]) -> dict:
        if not self.active:
            raise ValueError("coleta não iniciada; chame /collect/start")
        self.buffer.extend(packets)
        return {"posture": self.active, "recording": True, "samples": len(self.buffer), "xyz": self.xyz}

    def save(
        self,
        packets: list[CSIPacketIn] | None = None,
        meta: dict | None = None,
        xyz: dict[str, float] | None = None,
        profile: str | None = None,
        out_dir: Path | None = None,
    ) -> dict:
        if not self.active:
            raise ValueError("coleta não iniciada")
        if packets:
            self.buffer.extend(packets)
        if len(self.buffer) < 8:
            raise ValueError("poucos pacotes CSI; colete mais (~1–2s)")

        pose = self.active
        mat = packets_to_matrix(self.buffer)
        amp = mat[:, 0, :]
        phase = mat[:, 1, :]
        ts = int(time.time() * 1000)
        target_dir = Path(out_dir) if out_dir is not None else (self.out_dir or DATA_DIR)
        target_dir.mkdir(parents=True, exist_ok=True)
        while (target_dir / f"{pose}_{ts}.json").exists():
            ts += 1
        stem = target_dir / f"{pose}_{ts}"
        np.save(f"{stem}_amp.npy", amp.astype(np.float32))
        np.save(f"{stem}_phase.npy", phase.astype(np.float32))

        ref_xyz = xyz or self.xyz
        # defaults por postura se XYZ não informado
        if not ref_xyz:
            defaults = {
                "standing": {"x": 0.0, "y": 0.9, "z": 0.0},
                "sitting": {"x": 0.0, "y": 0.35, "z": 0.0},
                "lying": {"x": 0.0, "y": 0.2, "z": 0.0},
            }
            ref_xyz = defaults.get(pose, {"x": 0.0, "y": 0.0, "z": 0.0})

        payload = {
            "posture": pose,
            "packets": len(self.buffer),
            "shape_amp": list(amp.shape),
            "ts": ts,
            "xyz": ref_xyz,
            "profile": profile or self.profile or "adult",
            "meta": meta or {},
        }
        stem.with_suffix(".json").write_text(json.dumps(payload, indent=2))
        n = len(self.buffer)
        files = [f"{stem.name}_amp.npy", f"{stem.name}_phase.npy", f"{stem.name}.json"]
        self.active = None
        self.buffer = []
        self.started_at = None
        self.xyz = None
        self.profile = "adult"
        self.out_dir = None
        return {
            "saved": True,
            "posture": pose,
            "packets": n,
            "xyz": ref_xyz,
            "files": files,
        }

    def status(self) -> dict:
        counts = {"standing": 0, "sitting": 0, "lying": 0}
        for p in DATA_DIR.glob("*.json"):
            try:
                pose = json.loads(p.read_text()).get("posture")
                if pose in counts:
                    counts[pose] += 1
            except Exception:
                pass
        return {
            "recording": self.active is not None,
            "posture": self.active,
            "buffer": len(self.buffer),
            "xyz": self.xyz,
            "dataset": counts,
            "data_dir": str(DATA_DIR),
        }


collector = Collector()
