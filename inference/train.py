"""Treino local na GPU: Adam + CE(pose)+CE(profile)+MSE(xyz) -> csi_model_weights.pth."""
from __future__ import annotations

import argparse
import random
import shutil
from pathlib import Path

import torch
import torch.nn as nn
from torch.utils.data import DataLoader

from dataset import CSIDataset, augment_samples, load_samples
from device import resolve_device
from model import CSIPoseNet, POSE_LABELS, PROFILE_LABELS

ROOT = Path(__file__).resolve().parent
WEIGHTS_DIR = ROOT / "weights"
WEIGHTS_PRIMARY = WEIGHTS_DIR / "csi_model_weights.pth"
WEIGHTS_COMPAT = WEIGHTS_DIR / "model_weights.pth"


def train(epochs: int, batch: int, lr: float) -> None:
    device = resolve_device("cuda")
    print(f"device={device} cuda={torch.cuda.is_available()}")
    if device.type == "cuda":
        print(f"gpu={torch.cuda.get_device_name(0)}")

    samples = load_samples()
    if len(samples) < 6:
        raise SystemExit(
            f"dataset insuficiente ({len(samples)} janelas). "
            "Colete standing/sitting/lying via /collect e tente de novo."
        )

    random.shuffle(samples)
    n_val = max(1, len(samples) // 5)
    val_set = CSIDataset(samples[:n_val])
    train_set = CSIDataset(augment_samples(samples[n_val:]))
    train_loader = DataLoader(train_set, batch_size=batch, shuffle=True)
    val_loader = DataLoader(val_set, batch_size=batch)

    model = CSIPoseNet(feat=64, nodes=2).to(device)
    opt = torch.optim.Adam(model.parameters(), lr=lr)
    ce = nn.CrossEntropyLoss()
    mse = nn.MSELoss()

    best = 0.0
    WEIGHTS_DIR.mkdir(parents=True, exist_ok=True)

    for epoch in range(1, epochs + 1):
        model.train()
        total = 0.0
        for x, y, pidx, coord in train_loader:
            x = x.to(device)
            y = y.to(device)
            pidx = pidx.to(device)
            coord = coord.to(device)
            if x.dim() == 4 and x.shape[1] == 2 and x.shape[-1] <= 32:
                amp, phase = x[:, 0], x[:, 1]
                feat = torch.cat([amp, phase], dim=-1)
                x = torch.stack([feat, feat], dim=1)
            opt.zero_grad()
            pose_logits, profile_logits, pred_coord = model(x)
            loss = (
                ce(pose_logits, y)
                + 0.35 * ce(profile_logits, pidx)
                + 0.45 * mse(pred_coord, coord)
            )
            loss.backward()
            opt.step()
            total += float(loss.item()) * x.size(0)

        model.eval()
        correct = 0
        n = 0
        with torch.inference_mode():
            for x, y, _pidx, _coord in val_loader:
                x, y = x.to(device), y.to(device)
                if x.dim() == 4 and x.shape[1] == 2 and x.shape[-1] <= 32:
                    amp, phase = x[:, 0], x[:, 1]
                    feat = torch.cat([amp, phase], dim=-1)
                    x = torch.stack([feat, feat], dim=1)
                pose_logits, _, _ = model(x)
                pred = torch.argmax(pose_logits, dim=1)
                correct += int((pred == y).sum().item())
                n += y.size(0)
        acc = correct / max(1, n)
        print(f"epoch {epoch:03d} loss={total / max(1, len(train_set)):.4f} val_acc={acc:.3f}")

        if acc >= best:
            best = acc
            ckpt = {
                "model": model.state_dict(),
                "subcarriers": 32,
                "labels": list(POSE_LABELS),
                "profiles": list(PROFILE_LABELS),
                "val_acc": best,
            }
            torch.save(ckpt, WEIGHTS_PRIMARY)
            shutil.copyfile(WEIGHTS_PRIMARY, WEIGHTS_COMPAT)
            print(f"  saved {WEIGHTS_PRIMARY} val_acc={best:.3f}")

    print(f"done best_val_acc={best:.3f}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Treina CSIPoseNet multi-task")
    parser.add_argument("--epochs", type=int, default=40)
    parser.add_argument("--batch", type=int, default=16)
    parser.add_argument("--lr", type=float, default=1e-3)
    args = parser.parse_args()
    train(args.epochs, args.batch, args.lr)


if __name__ == "__main__":
    main()
