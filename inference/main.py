"""API FastAPI: coleta, mapeamento espacial, calibração postural e /predict."""
from __future__ import annotations

import os

from fastapi import FastAPI, HTTPException

from calibration import CalibrationError
from calibration_session import guided
from calibration_store import (
    POSITION_JUMP_WARNING_M,
    active_summary,
    activate as activate_calibration,
    discard as discard_calibration,
    list_calibrations,
    load_active_on_startup,
    load_calibration,
)
from collector import collector
from device import device_info
from mapping import spatial_mapper
from schemas import (
    CSIPacketIn,
    CalibrationMarkStart,
    CalibrationPackets,
    CalibrationSessionCreate,
    CalibrateRequest,
    CalibrateResponse,
    CalibrateStatus,
    CollectSave,
    CollectStart,
    MapCalibrateRequest,
    PredictRequest,
    PredictResponse,
)
from service import InferenceService
from stabilize import stabilizer
from pydantic import BaseModel, Field

PORT = int(os.getenv("PORT", "8790"))

app = FastAPI(title="scan-wifi CSI pose inference", version="2.0.0")
svc = InferenceService()
load_active_on_startup(spatial_mapper)


class Health(BaseModel):
    status: str
    device: dict
    weights_loaded: bool
    weights_path: str | None = None
    spatial_ready: bool = False


class CollectPush(BaseModel):
    packets: list[CSIPacketIn] = Field(default_factory=list)


@app.get("/health")
def health() -> Health:
    return Health(
        status="ok",
        device=device_info(svc.device),
        weights_loaded=svc.weights_loaded,
        weights_path=svc.weights_path,
        spatial_ready=spatial_mapper.ready(),
    )


@app.post("/predict", response_model=PredictResponse)
def predict(body: PredictRequest) -> PredictResponse:
    if not body.packets:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": "packets vazio"})
    try:
        return svc.predict(body.packets, body.room, packets2=body.packets2 or None, stereo=body.stereo)
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e


@app.post("/calibrate", response_model=CalibrateResponse)
def calibrate(body: CalibrateRequest) -> CalibrateResponse:
    if not body.packets:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": "packets vazio"})
    try:
        posture, samples, ready, profiles = svc.calibrate(body.posture, body.packets)
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e
    return CalibrateResponse(posture=posture, samples=samples, ready=ready, profiles=profiles)


@app.get("/calibrate/status", response_model=CalibrateStatus)
def calibrate_status() -> CalibrateStatus:
    return CalibrateStatus(
        ready=svc.calibrator.ready(),
        profiles=svc.calibrator.status(),
        needed=int(os.getenv("CALIBRATE_PACKETS", "50")),
        device=str(svc.device),
    )


@app.post("/map/calibrate")
def map_calibrate(body: MapCalibrateRequest) -> dict:
    try:
        return spatial_mapper.calibrate(
            body.packets,
            x=body.x,
            z=body.z,
            y=body.y,
            label=body.label,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e


@app.get("/map/status")
def map_status() -> dict:
    return spatial_mapper.status()


@app.post("/map/reset")
def map_reset() -> dict:
    spatial_mapper.clear()
    stabilizer.reset()
    return {"ok": True, **spatial_mapper.status()}


@app.post("/collect/start")
def collect_start(body: CollectStart) -> dict:
    try:
        return collector.start(body.posture, x=body.x, y=body.y, z=body.z, profile=getattr(body, "profile", "adult") or "adult")
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e


@app.post("/collect/push")
def collect_push(body: CollectPush) -> dict:
    try:
        return collector.push(body.packets)
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e


@app.post("/collect/save")
def collect_save(body: CollectSave) -> dict:
    try:
        if body.posture and not collector.active:
            collector.start(
                body.posture,
                x=body.x,
                y=body.y,
                z=body.z,
                profile=body.profile or "adult",
            )
        meta = {}
        if body.room:
            meta["room"] = body.room.model_dump()
        xyz = None
        if body.x is not None and body.y is not None and body.z is not None:
            xyz = {"x": body.x, "y": body.y, "z": body.z}
        return collector.save(
            body.packets or None,
            meta=meta,
            xyz=xyz,
            profile=body.profile,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail={"code": "BAD_REQUEST", "message": str(e)}) from e


@app.get("/collect/status")
def collect_status() -> dict:
    return collector.status()


def calibration_http_error(error: CalibrationError) -> HTTPException:
    return HTTPException(status_code=error.status, detail={"code": error.code, "message": error.message})


@app.post("/calibration/session")
def calibration_session_create(body: CalibrationSessionCreate) -> dict:
    try:
        return guided.create([m.model_dump() for m in body.marks], dict(body.room))
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.get("/calibration/session")
def calibration_session_status() -> dict:
    try:
        return guided.snapshot()
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.post("/calibration/marks/{label}/start")
def calibration_mark_start(label: str, body: CalibrationMarkStart) -> dict:
    try:
        return guided.start_mark(label, body.posture)
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.post("/calibration/marks/{label}/packets")
def calibration_mark_packets(label: str, body: CalibrationPackets) -> dict:
    try:
        return guided.push_packets(label, body.packets)
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.post("/calibration/marks/{label}/skip")
def calibration_mark_skip(label: str) -> dict:
    try:
        return guided.skip_mark(label)
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.delete("/calibration/marks/{label}/samples")
def calibration_mark_clear(label: str) -> dict:
    try:
        return guided.clear_samples(label)
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.post("/calibration/compute")
def calibration_compute() -> dict:
    try:
        return guided.compute()
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.get("/calibration/list")
def calibration_list() -> dict:
    return {"items": list_calibrations(), "active": active_summary(spatial_mapper)}


@app.get("/calibration/active")
def calibration_active() -> dict:
    return {"active": active_summary(spatial_mapper)}


@app.get("/calibration/{calibration_id}")
def calibration_detail(calibration_id: str) -> dict:
    try:
        return load_calibration(calibration_id)
    except CalibrationError as e:
        raise calibration_http_error(e) from e


@app.post("/calibration/{calibration_id}/activate")
def calibration_activate(calibration_id: str) -> dict:
    try:
        _, jump = activate_calibration(calibration_id, spatial_mapper)
    except CalibrationError as e:
        raise calibration_http_error(e) from e
    return {
        "active": active_summary(spatial_mapper),
        "position_jump_m": None if jump is None else round(jump, 3),
        "position_jump_warning": jump is not None and jump > POSITION_JUMP_WARNING_M,
    }


@app.post("/calibration/{calibration_id}/discard")
def calibration_discard(calibration_id: str) -> dict:
    try:
        payload = discard_calibration(calibration_id, spatial_mapper)
    except CalibrationError as e:
        raise calibration_http_error(e) from e
    return {"id": payload["id"], "status": payload["status"]}


@app.post("/weights/reload")
def weights_reload() -> dict:
    ok = svc.load_weights()
    return {
        "weights_loaded": ok,
        "path": svc.weights_path or "weights/csi_model_weights.pth",
    }


@app.post("/stabilize/reset")
def stabilize_reset() -> dict:
    stabilizer.reset()
    return {"ok": True}


def run() -> None:
    import uvicorn

    host = os.getenv("HOST_BIND", "127.0.0.1")
    uvicorn.run("main:app", host=host, port=PORT, reload=False)


if __name__ == "__main__":
    run()
