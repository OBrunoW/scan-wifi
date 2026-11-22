"""Compat: reexporta model.py."""
from __future__ import annotations

import sys
from pathlib import Path

_root = Path(__file__).resolve().parent.parent
if str(_root) not in sys.path:
    sys.path.insert(0, str(_root))

from model import CSIPoseNet, POSE_LABELS, POSE_TO_IDX, PROFILE_LABELS, PROFILE_TO_IDX, build_model

__all__ = [
    "CSIPoseNet",
    "POSE_LABELS",
    "POSE_TO_IDX",
    "PROFILE_LABELS",
    "PROFILE_TO_IDX",
    "build_model",
]
