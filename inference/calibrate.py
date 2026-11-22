from __future__ import annotations

import os
from collections import defaultdict

import numpy as np
import torch
import torch.nn as nn

from features import signature_vector
from schemas import CSIPacketIn, Posture

NEEDED = int(os.getenv("CALIBRATE_PACKETS", "50"))
POSE_ORDER: tuple[Posture, ...] = ("standing", "sitting", "lying")


class Calibrator:
    """Perfis de assinatura CSI por postura + MLP leve treinado na hora."""

    def __init__(self, device: torch.device):
        self.device = device
        self.profiles: dict[Posture, list[np.ndarray]] = defaultdict(list)
        self.means: dict[Posture, np.ndarray] = {}
        self.mlp: nn.Module | None = None
        self.feat_dim = 0

    def add(self, posture: Posture, packets: list[CSIPacketIn]) -> int:
        if len(packets) < max(5, NEEDED // 5):
            raise ValueError(f"envie ao menos {max(5, NEEDED // 5)} pacotes CSI")
        use = packets[-NEEDED:] if len(packets) > NEEDED else packets
        vec = signature_vector(use)
        self.profiles[posture].append(vec)
        self.means[posture] = np.mean(np.stack(self.profiles[posture], axis=0), axis=0)
        self.feat_dim = int(vec.shape[0])
        self._maybe_train()
        return len(use)

    def status(self) -> dict[str, int]:
        return {p: len(self.profiles.get(p, [])) for p in POSE_ORDER}

    def ready(self) -> bool:
        return all(len(self.profiles.get(p, [])) >= 1 for p in POSE_ORDER)

    def _maybe_train(self) -> None:
        if not self.ready() or self.feat_dim == 0:
            self.mlp = None
            return
        xs: list[np.ndarray] = []
        ys: list[int] = []
        for i, pose in enumerate(POSE_ORDER):
            for v in self.profiles[pose]:
                xs.append(v)
                ys.append(i)
            xs.append(self.means[pose])
            ys.append(i)
        x = torch.tensor(np.stack(xs), dtype=torch.float32, device=self.device)
        y = torch.tensor(ys, dtype=torch.long, device=self.device)
        model = nn.Sequential(
            nn.Linear(self.feat_dim, 64),
            nn.ReLU(),
            nn.Linear(64, 32),
            nn.ReLU(),
            nn.Linear(32, 3),
        ).to(self.device)
        opt = torch.optim.Adam(model.parameters(), lr=1e-2)
        loss_fn = nn.CrossEntropyLoss()
        model.train()
        for _ in range(80):
            opt.zero_grad()
            logits = model(x)
            loss = loss_fn(logits, y)
            loss.backward()
            opt.step()
        model.eval()
        self.mlp = model

    @torch.inference_mode()
    def classify(self, packets: list[CSIPacketIn]) -> tuple[Posture, float]:
        if not self.means:
            return "standing", 0.0
        vec = signature_vector(packets)

        if self.mlp is not None and self.ready():
            t = torch.tensor(vec[None, :], dtype=torch.float32, device=self.device)
            logits = self.mlp(t)[0]
            prob = torch.softmax(logits, dim=0)
            idx = int(torch.argmax(prob).item())
            return POSE_ORDER[idx], float(prob[idx].item())

        best_pose: Posture = "standing"
        best_dist = float("inf")
        dists: dict[Posture, float] = {}
        for pose, mean in self.means.items():
            d = float(np.linalg.norm(vec - mean))
            dists[pose] = d
            if d < best_dist:
                best_dist = d
                best_pose = pose
        vals = np.array([dists.get(p, best_dist + 1.0) for p in POSE_ORDER], dtype=np.float64)
        scores = np.exp(-vals)
        scores /= scores.sum() + 1e-9
        conf = float(scores[POSE_ORDER.index(best_pose)])
        return best_pose, conf
