from __future__ import annotations

import os

import torch


def resolve_device(prefer: str | None = None) -> torch.device:
    want = (prefer or os.getenv("DEVICE", "cuda")).strip().lower()
    if want.startswith("cuda") and torch.cuda.is_available():
        return torch.device("cuda")
    return torch.device("cpu")


def device_info(dev: torch.device) -> dict:
    info = {"device": str(dev), "cuda_available": torch.cuda.is_available()}
    if dev.type == "cuda":
        info["name"] = torch.cuda.get_device_name(0)
        props = torch.cuda.get_device_properties(0)
        info["vram_gb"] = round(props.total_memory / (1024**3), 2)
    return info
