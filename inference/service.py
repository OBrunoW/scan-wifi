"""Inferencia em tempo real: CNN + mapeamento espacial + estabilizacao."""
from __future__ import annotations

import logging
import os
from pathlib import Path

import numpy as np
import torch

from calibrate import Calibrator
from device import resolve_device
from features import signature_vector, stereo_window_tensor, zone_coords
from mapping import triangulate
from mapping import spatial_mapper
from model import POSE_LABELS, PROFILE_LABELS, build_model
from schemas import CSIPacketIn, PersonOut, PredictResponse, RoomIn, normalize_posture
from stabilize import stabilizer

logger = logging.getLogger(__name__)
WINDOW_SIZE = int(os.getenv("WINDOW_SIZE", "32"))
ROOT = Path(__file__).resolve().parent
WEIGHTS_CANDIDATES = [
    ROOT / "weights" / "csi_model_weights.pth",
    ROOT / "weights" / "model_weights.pth",
]


def local_posture(energy: float) -> str:
    if energy < 0.045:
        return "lying"
    if energy < 0.075:
        return "sitting"
    return "standing"


def heuristic_profile(energy: float, y: float) -> str:
    vol = float(energy) + abs(float(y)) * 0.15
    if vol < 0.05:
        return "pet"
    if vol < 0.09:
        return "child"
    return "adult"


def _decode_xyz(raw: np.ndarray) -> tuple[float, float, float]:
    return float(raw[0]), float(raw[1]), float(raw[2])


class InferenceService:
    def __init__(self) -> None:
        self.device = resolve_device()
        self.model = build_model(self.device)
        self.calibrator = Calibrator(self.device)
        self.weights_loaded = False
        self.weights_path: str | None = None
        self.load_weights()

    def load_weights(self) -> bool:
        for path in WEIGHTS_CANDIDATES:
            if not path.exists():
                continue
            try:
                ckpt = torch.load(path, map_location=self.device, weights_only=False)
                state = ckpt["model"] if isinstance(ckpt, dict) and "model" in ckpt else ckpt
                missing, unexpected = self.model.load_state_dict(state, strict=False)
                self.model.eval()
                self.weights_loaded = True
                self.weights_path = str(path)
                if missing:
                    print(f"weights partial missing={list(missing)[:4]}...")
                if unexpected:
                    print(f"weights unexpected={list(unexpected)[:4]}...")
                return True
            except Exception as e:
                print(f"weights load fail {path}: {e}")
                continue
        self.weights_loaded = False
        self.weights_path = None
        return False

    def calibrate(self, posture: str, packets: list[CSIPacketIn]) -> tuple[str, int, bool, dict[str, int]]:
        pose = normalize_posture(posture)
        n = self.calibrator.add(pose, packets)
        return pose, n, self.calibrator.ready(), self.calibrator.status()

    @torch.inference_mode()
    def predict(self, packets: list[CSIPacketIn], room: RoomIn, packets2: list[CSIPacketIn] | None = None, stereo: bool = False) -> PredictResponse:
        zones = zone_coords(packets, room.width, room.depth, room.esp_z)
        calibrated = self.calibrator.ready() or spatial_mapper.ready()

        global_pose = "standing"
        global_profile = "adult"
        conf = 0.35
        source = "heuristic"
        cnn_xyz: tuple[float, float, float] | None = None

        if self.calibrator.ready() or self.calibrator.means:
            global_pose, conf = self.calibrator.classify(packets)
            source = "calibrator"

        try:
            x_t = torch.tensor(
                stereo_window_tensor(packets, packets2 if stereo or packets2 else None, WINDOW_SIZE),
                device=self.device,
            )
            pose_logits, profile_logits, coords = self.model(x_t)
            pose_prob = torch.softmax(pose_logits[0], dim=0)
            pidx = int(torch.argmax(pose_prob).item())
            cnn_pose = POSE_LABELS[pidx]
            cnn_conf = float(pose_prob[pidx].item())
            prof_prob = torch.softmax(profile_logits[0], dim=0)
            fidx = int(torch.argmax(prof_prob).item())
            cnn_profile = PROFILE_LABELS[fidx]
            raw = coords[0].detach().cpu().numpy()
            cnn_xyz = _decode_xyz(raw)
            if self.weights_loaded and cnn_conf > 0.4:
                global_pose, conf, source = cnn_pose, cnn_conf, "cnn_weights"
                global_profile = cnn_profile
            elif not calibrated and cnn_conf > 0.55:
                global_pose, conf, source = cnn_pose, cnn_conf, "cnn"
                global_profile = cnn_profile
            elif calibrated and cnn_conf > conf + 0.12:
                global_pose, conf, source = cnn_pose, cnn_conf, "cnn+map"
                global_profile = cnn_profile
        except Exception:
            logger.exception("inferencia CNN falhou; usando heuristica")

        persons: list[PersonOut] = []
        if zones:
            primary = int(np.argmax([e for *_, e in zones]))
            for i, (zx, zy, zz, e) in enumerate(zones):
                pid = i + 1
                if i == primary and conf >= 0.4:
                    posture = global_pose
                    profile = global_profile
                    c = float(np.clip(conf, 0.05, 0.99))
                else:
                    posture = local_posture(float(e))
                    profile = heuristic_profile(float(e), zy)
                    c = float(np.clip(0.35 + min(e, 0.1), 0.05, 0.7))

                px, py, pz = zx, zy, zz
                if cnn_xyz is not None and i == primary:
                    px = 0.55 * zx + 0.45 * cnn_xyz[0]
                    py = 0.55 * zy + 0.45 * cnn_xyz[1]
                    pz = 0.55 * zz + 0.45 * cnn_xyz[2]

                if i == primary and (stereo or packets2):
                    tx, tz, tconf = triangulate(packets, packets2, room)
                    px = 0.35 * px + 0.65 * tx
                    pz = 0.35 * pz + 0.65 * tz
                    c = max(c, tconf)
                    source = f"{source}+tri" if "tri" not in source else source

                if i == primary and spatial_mapper.ready():
                    mx, mz, mconf = spatial_mapper.map_xz(packets, fallback=(px, pz))
                    blend = 0.35 + 0.55 * mconf
                    px = (1 - blend) * px + blend * mx
                    pz = (1 - blend) * pz + blend * mz
                    c = max(c, mconf * 0.85)
                    if source == "heuristic":
                        source = "spatial_map"
                    elif "map" not in source:
                        source = f"{source}+map"

                hw, hd = room.width * 0.48, room.depth * 0.48
                px = float(np.clip(px, -hw, hw))
                pz = float(np.clip(pz, -hd, hd))

                if profile == "adult" and i != primary:
                    profile = heuristic_profile(float(e), py)

                posture, px, py, pz, profile = stabilizer.apply(
                    pid, posture, px, py, pz, profile=profile
                )
                persons.append(
                    PersonOut(
                        id=pid,
                        posture=posture,
                        profile=profile,
                        x=round(px, 3),
                        y=round(py, 3),
                        z=round(pz, 3),
                        confidence=round(c, 3),
                    )
                )
        else:
            sig = signature_vector(packets)
            energy = float(np.linalg.norm(sig[: len(sig) // 2]))
            if calibrated or self.weights_loaded or energy > 0.5:
                x = cnn_xyz[0] if cnn_xyz else 0.0
                y = cnn_xyz[1] if cnn_xyz else 0.0
                z = cnn_xyz[2] if cnn_xyz else room.esp_z * 0.2
                if spatial_mapper.ready():
                    mx, mz, _ = spatial_mapper.map_xz(packets, fallback=(x, z))
                    x, z = mx, mz
                    source = f"{source}+map" if "map" not in source else source
                profile = global_profile if self.weights_loaded else heuristic_profile(energy, y)
                posture, x, y, z, profile = stabilizer.apply(
                    1, global_pose, x, y, z, profile=profile
                )
                persons.append(
                    PersonOut(
                        id=1,
                        posture=posture,
                        profile=profile,
                        x=round(x, 3),
                        y=round(y, 3),
                        z=round(z, 3),
                        confidence=round(max(conf, 0.2), 3),
                    )
                )

        return PredictResponse(
            persons=persons,
            calibrated=calibrated or self.weights_loaded,
            source=source,
            device=str(self.device),
        )
