"""Rede multi-task CSI stereo: postura + perfil + XYZ."""
from __future__ import annotations

import torch
import torch.nn as nn

POSE_LABELS = ("standing", "sitting", "lying")
POSE_TO_IDX = {p: i for i, p in enumerate(POSE_LABELS)}
POSE_ALIASES = {
    "standing": "standing",
    "sitting": "sitting",
    "seated": "sitting",
    "lying": "lying",
}

PROFILE_LABELS = ("adult", "child", "pet")
PROFILE_TO_IDX = {p: i for i, p in enumerate(PROFILE_LABELS)}
PROFILE_ALIASES = {
    "adult": "adult",
    "adulto": "adult",
    "child": "child",
    "crianca": "child",
    "kid": "child",
    "pet": "pet",
    "animal": "pet",
    "dog": "pet",
    "cao": "pet",
}


class CSIPoseNet(nn.Module):
    """Aceita stereo (B, 2, T, 64) = 2 nos x 64 feats (amp+phase)."""

    def __init__(self, feat: int = 64, hidden: int = 96, nodes: int = 2):
        super().__init__()
        self.feat = feat
        self.nodes = nodes
        in_ch = nodes * feat
        self.backbone = nn.Sequential(
            nn.Conv1d(in_ch, 64, kernel_size=5, padding=2),
            nn.BatchNorm1d(64),
            nn.ReLU(inplace=True),
            nn.Conv1d(64, 80, kernel_size=3, padding=1),
            nn.BatchNorm1d(80),
            nn.ReLU(inplace=True),
            nn.Conv1d(80, 96, kernel_size=3, padding=1),
            nn.BatchNorm1d(96),
            nn.ReLU(inplace=True),
            nn.AdaptiveAvgPool1d(1),
        )
        self.fc = nn.Sequential(
            nn.Linear(96, hidden),
            nn.ReLU(inplace=True),
            nn.Dropout(0.15),
        )
        self.pose_head = nn.Linear(hidden, len(POSE_LABELS))
        self.profile_head = nn.Linear(hidden, len(PROFILE_LABELS))
        self.coord_head = nn.Sequential(
            nn.Linear(hidden, 48),
            nn.ReLU(inplace=True),
            nn.Linear(48, 3),
        )

    def forward(self, x: torch.Tensor) -> tuple[torch.Tensor, torch.Tensor, torch.Tensor]:
        if x.dim() != 4:
            raise ValueError(f"esperado (B,N,T,F) got {tuple(x.shape)}")
        b, n, t, f = x.shape
        if f != self.feat:
            if f > self.feat:
                x = x[..., : self.feat]
            else:
                x = torch.nn.functional.pad(x, (0, self.feat - f))
            f = self.feat
        if n < self.nodes:
            pad = self.nodes - n
            x = torch.cat([x, x[:, :1].repeat(1, pad, 1, 1)], dim=1)
            n = self.nodes
        elif n > self.nodes:
            x = x[:, : self.nodes]
            n = self.nodes
        x = x.permute(0, 1, 3, 2).contiguous().view(b, n * f, t)
        h = self.backbone(x).squeeze(-1)
        h = self.fc(h)
        return self.pose_head(h), self.profile_head(h), self.coord_head(h)


def build_model(device: torch.device, feat: int = 64) -> CSIPoseNet:
    model = CSIPoseNet(feat=feat).to(device)
    model.eval()
    return model
