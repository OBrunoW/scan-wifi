from __future__ import annotations

import json
import logging
import os
import tempfile
from pathlib import Path

from calibration import CalibrationError
from collector import DATA_DIR
from mapping import ActiveCalibration, SpatialMapper

logger = logging.getLogger(__name__)

CALIBRATION_DIR = DATA_DIR / "calibration"
ACTIVE_FILE = "active.json"
CALIBRATION_FILE = "calibration.json"
POSITION_JUMP_WARNING_M = 1.0
STATUS_ACTIVE = "ativa"
STATUS_INACTIVE = "inativa"
STATUS_DISCARDED = "descartada"


def write_json_atomic(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    handle, temp_name = tempfile.mkstemp(dir=path.parent, prefix=f".{path.name}.", suffix=".tmp")
    try:
        with os.fdopen(handle, "w") as stream:
            json.dump(payload, stream, indent=2)
        os.replace(temp_name, path)
    except OSError:
        Path(temp_name).unlink(missing_ok=True)
        raise


def calibration_file(calibration_id: str, root: Path = CALIBRATION_DIR) -> Path:
    return root / calibration_id / CALIBRATION_FILE


def valid_calibration_id(calibration_id: str) -> bool:
    return bool(calibration_id) and calibration_id == Path(calibration_id).name and not calibration_id.startswith(".")


def save_calibration(payload: dict, root: Path = CALIBRATION_DIR) -> None:
    write_json_atomic(calibration_file(payload["id"], root), payload)


def load_calibration(calibration_id: str, root: Path = CALIBRATION_DIR) -> dict:
    if not valid_calibration_id(calibration_id):
        raise CalibrationError("BAD_REQUEST", "id de calibracao invalido")
    path = calibration_file(calibration_id, root)
    if not path.exists():
        raise CalibrationError("CALIBRATION_NOT_FOUND", f"calibracao {calibration_id} nao existe", status=404)
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise CalibrationError("CALIBRATION_CORRUPT", f"calibracao {calibration_id} ilegivel: {error}", status=500) from error


def summarize(payload: dict) -> dict:
    lomo = payload.get("lomo") or {}
    marks = payload.get("marks") or []
    return {
        "id": payload.get("id"),
        "created_at": payload.get("created_at"),
        "status": payload.get("status"),
        "marks": sum(1 for m in marks if m.get("complete")),
        "windows": lomo.get("windows", 0),
        "lomo_mean_m": lomo.get("mean_m"),
        "lomo_p90_m": lomo.get("p90_m"),
        "baseline": payload.get("baseline"),
    }


def list_calibrations(root: Path = CALIBRATION_DIR) -> list[dict]:
    if not root.exists():
        return []
    items = []
    for directory in sorted(root.iterdir()):
        if not directory.is_dir():
            continue
        try:
            items.append(summarize(load_calibration(directory.name, root)))
        except CalibrationError:
            logger.exception("calibration %s unreadable; skipped in listing", directory.name)
    return sorted(items, key=lambda item: item.get("created_at") or 0, reverse=True)


def read_active_id(root: Path = CALIBRATION_DIR) -> str | None:
    path = root / ACTIVE_FILE
    if not path.exists():
        return None
    try:
        value = json.loads(path.read_text()).get("id")
    except (OSError, json.JSONDecodeError, AttributeError) as error:
        raise CalibrationError("CALIBRATION_CORRUPT", f"active.json ilegivel: {error}", status=500) from error
    return value or None


def write_active_id(calibration_id: str, root: Path = CALIBRATION_DIR) -> None:
    write_json_atomic(root / ACTIVE_FILE, {"id": calibration_id})


def set_status(calibration_id: str, status: str, root: Path = CALIBRATION_DIR) -> dict:
    payload = load_calibration(calibration_id, root)
    payload["status"] = status
    save_calibration(payload, root)
    return payload


def to_active(payload: dict) -> ActiveCalibration:
    try:
        return ActiveCalibration(
            id=payload["id"],
            anchors=list(payload["anchors"]),
            posture_profiles=dict(payload.get("posture_profiles") or {}),
            lomo=dict(payload.get("lomo") or {}),
            created_at=int(payload.get("created_at") or 0),
        )
    except (KeyError, TypeError, ValueError) as error:
        raise CalibrationError("CALIBRATION_CORRUPT", f"calibracao mal formada: {error}", status=500) from error


def activate(
    calibration_id: str, mapper: SpatialMapper, root: Path = CALIBRATION_DIR
) -> tuple[dict, float | None]:
    payload = load_calibration(calibration_id, root)
    active = to_active(payload)
    if len(active.anchors) < 2:
        raise CalibrationError("CALIBRATION_CORRUPT", "calibracao sem ancoras suficientes", status=500)

    previous_id = read_active_id(root)
    if previous_id and previous_id != calibration_id:
        try:
            set_status(previous_id, STATUS_INACTIVE, root)
        except CalibrationError:
            logger.exception("previous calibration %s could not be marked inactive", previous_id)

    payload["status"] = STATUS_ACTIVE
    save_calibration(payload, root)
    write_active_id(calibration_id, root)
    jump = mapper.position_jump(active)
    mapper.set_guided(active)
    return payload, jump


def discard(calibration_id: str, mapper: SpatialMapper, root: Path = CALIBRATION_DIR) -> dict:
    payload = load_calibration(calibration_id, root)
    if mapper.guided is not None and mapper.guided.id == calibration_id:
        raise CalibrationError("BAD_REQUEST", "calibracao ativa nao pode ser descartada; ative outra antes", status=409)
    payload["status"] = STATUS_DISCARDED
    save_calibration(payload, root)
    return payload


def active_summary(mapper: SpatialMapper) -> dict | None:
    guided = mapper.guided
    if guided is None:
        return None
    return {
        "id": guided.id,
        "created_at": guided.created_at,
        "lomo_mean_m": guided.lomo.get("mean_m"),
        "lomo_p90_m": guided.lomo.get("p90_m"),
        "marks": len(guided.anchors),
        "postures_ready": all(p in guided.posture_profiles for p in ("standing", "sitting", "lying")),
    }


def load_active_on_startup(mapper: SpatialMapper, root: Path = CALIBRATION_DIR) -> ActiveCalibration | None:
    try:
        calibration_id = read_active_id(root)
        if calibration_id is None:
            return None
        active = to_active(load_calibration(calibration_id, root))
    except CalibrationError:
        logger.exception("active calibration could not be loaded; starting without one")
        return None
    mapper.set_guided(active)
    logger.info("active calibration loaded: %s", active.id)
    return active
