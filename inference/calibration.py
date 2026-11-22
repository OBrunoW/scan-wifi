from __future__ import annotations

import math
from collections import defaultdict
from dataclasses import dataclass, field, replace

import numpy as np

from mapping import weighted_position

POSTURES: tuple[str, ...] = ("standing", "sitting", "lying")
GOAL_WINDOWS = 15
MIN_COMPLETE_MARKS = 4
MIN_MARKS_PER_POSTURE = 2
OUTLIER_SIGMA = 3.0
OUTLIER_MIN_WINDOWS = 5
MIN_ANCHORS_FOR_LOMO = 2


class CalibrationError(Exception):
    def __init__(self, code: str, message: str, status: int = 400) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status


@dataclass(frozen=True)
class Window:
    label: str
    x: float
    z: float
    posture: str
    signature: np.ndarray
    discarded: bool = False


@dataclass(frozen=True)
class MarkStats:
    label: str
    x: float
    z: float
    collected: dict[str, int]
    accepted: dict[str, int]
    posture_means: dict[str, np.ndarray]
    signature: np.ndarray
    signatures: list[np.ndarray] = field(default_factory=list)
    complete: bool = False


@dataclass(frozen=True)
class LomoReport:
    mean_m: float
    p90_m: float
    per_mark: dict[str, float]
    windows: int
    marks: int


def flag_outliers(
    windows: list[Window],
    sigma: float = OUTLIER_SIGMA,
    min_windows: int = OUTLIER_MIN_WINDOWS,
) -> list[Window]:
    groups: dict[tuple[str, str], list[int]] = defaultdict(list)
    for index, window in enumerate(windows):
        groups[(window.label, window.posture)].append(index)

    result = list(windows)
    for indexes in groups.values():
        if len(indexes) < min_windows:
            continue
        vectors = np.stack([windows[i].signature for i in indexes])
        median = np.median(vectors, axis=0)
        distances = np.linalg.norm(vectors - median, axis=1)
        threshold = float(distances.mean() + sigma * distances.std())
        for index, distance in zip(indexes, distances):
            if float(distance) > threshold:
                result[index] = replace(windows[index], discarded=True)
    return result


def marks_from_windows(windows: list[Window], goal: int = GOAL_WINDOWS) -> dict[str, MarkStats]:
    by_label: dict[str, list[Window]] = defaultdict(list)
    for window in windows:
        by_label[window.label].append(window)

    marks: dict[str, MarkStats] = {}
    for label, items in by_label.items():
        collected = {p: 0 for p in POSTURES}
        accepted = {p: 0 for p in POSTURES}
        vectors: dict[str, list[np.ndarray]] = defaultdict(list)
        for window in items:
            collected[window.posture] += 1
            if window.discarded:
                continue
            accepted[window.posture] += 1
            vectors[window.posture].append(window.signature)
        if not vectors:
            continue
        posture_means = {p: np.mean(np.stack(v), axis=0) for p, v in vectors.items()}
        signature = np.mean(np.stack(list(posture_means.values())), axis=0)
        marks[label] = MarkStats(
            label=label,
            x=items[0].x,
            z=items[0].z,
            collected=collected,
            accepted=accepted,
            posture_means=posture_means,
            signature=signature,
            signatures=[w.signature for w in items if not w.discarded],
            complete=any(count >= goal for count in collected.values()),
        )
    return marks


def complete_marks(marks: dict[str, MarkStats]) -> list[MarkStats]:
    return sorted((m for m in marks.values() if m.complete), key=lambda m: m.label)


def ensure_enough_marks(marks: dict[str, MarkStats], minimum: int = MIN_COMPLETE_MARKS) -> list[MarkStats]:
    complete = complete_marks(marks)
    if len(complete) < minimum:
        raise CalibrationError(
            "CALIBRATION_TOO_FEW_MARKS",
            f"{len(complete)} marcos completos; minimo {minimum}",
            status=409,
        )
    return complete


def mark_anchor(mark: MarkStats) -> dict:
    return {
        "label": mark.label,
        "x": float(mark.x),
        "y": 0.0,
        "z": float(mark.z),
        "signature": mark.signature.astype(float).tolist(),
    }


def position_anchors(marks: dict[str, MarkStats]) -> list[dict]:
    return [mark_anchor(m) for m in complete_marks(marks)]


def posture_profiles(
    marks: dict[str, MarkStats],
    min_marks: int = MIN_MARKS_PER_POSTURE,
) -> dict[str, np.ndarray]:
    profiles: dict[str, np.ndarray] = {}
    for posture in POSTURES:
        means = [m.posture_means[posture] for m in complete_marks(marks) if posture in m.posture_means]
        if len(means) >= min_marks:
            profiles[posture] = np.mean(np.stack(means), axis=0)
    return profiles


def profiles_ready(profiles: dict[str, np.ndarray]) -> bool:
    return all(posture in profiles for posture in POSTURES)


def _summarize(errors: list[float]) -> tuple[float, float]:
    if not errors:
        return 0.0, 0.0
    values = np.asarray(errors, dtype=np.float64)
    return float(values.mean()), float(np.percentile(values, 90))


def lomo_error(marks: dict[str, MarkStats]) -> LomoReport:
    complete = complete_marks(marks)
    if len(complete) <= MIN_ANCHORS_FOR_LOMO:
        raise CalibrationError(
            "CALIBRATION_TOO_FEW_MARKS",
            f"LOMO exige mais de {MIN_ANCHORS_FOR_LOMO} marcos completos",
            status=409,
        )

    all_errors: list[float] = []
    per_mark: dict[str, float] = {}
    for held_out in complete:
        anchors = [mark_anchor(m) for m in complete if m.label != held_out.label]
        errors = []
        for signature in held_out.signatures:
            x, z, _ = weighted_position(signature, anchors)
            errors.append(math.hypot(x - held_out.x, z - held_out.z))
        if not errors:
            continue
        per_mark[held_out.label] = float(np.mean(errors))
        all_errors.extend(errors)

    mean_m, p90_m = _summarize(all_errors)
    return LomoReport(
        mean_m=mean_m,
        p90_m=p90_m,
        per_mark=per_mark,
        windows=len(all_errors),
        marks=len(per_mark),
    )


def baseline_error(
    marks: dict[str, MarkStats],
    positions: dict[str, list[tuple[float, float]]],
) -> dict:
    errors: list[float] = []
    per_mark: dict[str, float] = {}
    for label, points in positions.items():
        mark = marks.get(label)
        if mark is None or not points:
            continue
        mark_errors = [math.hypot(x - mark.x, z - mark.z) for x, z in points]
        per_mark[label] = float(np.mean(mark_errors))
        errors.extend(mark_errors)
    mean_m, p90_m = _summarize(errors)
    return {"mean_m": mean_m, "p90_m": p90_m, "per_mark": per_mark, "samples": len(errors)}


def improvement_ratio(lomo_mean_m: float, baseline_mean_m: float) -> float | None:
    if baseline_mean_m <= 0:
        return None
    return float((baseline_mean_m - lomo_mean_m) / baseline_mean_m)


def build_calibration_payload(
    calibration_id: str,
    created_at: int,
    room: dict,
    mark_defs: list[dict],
    skipped: set[str],
    marks: dict[str, MarkStats],
    report: LomoReport,
    profiles: dict[str, np.ndarray],
    status: str = "calculada",
) -> dict:
    mark_rows = []
    for definition in mark_defs:
        label = definition["label"]
        stats = marks.get(label)
        mark_rows.append(
            {
                "label": label,
                "x": float(definition["x"]),
                "z": float(definition["z"]),
                "postures": {p: n for p, n in (stats.accepted if stats else {}).items() if n > 0},
                "complete": bool(stats and stats.complete),
                "skipped": label in skipped,
            }
        )
    return {
        "id": calibration_id,
        "created_at": created_at,
        "room": room,
        "marks": mark_rows,
        "anchors": position_anchors(marks),
        "posture_profiles": {p: v.astype(float).tolist() for p, v in profiles.items()},
        "lomo": {
            "mean_m": report.mean_m,
            "p90_m": report.p90_m,
            "per_mark": report.per_mark,
            "windows": report.windows,
            "marks": report.marks,
        },
        "baseline": None,
        "status": status,
    }
