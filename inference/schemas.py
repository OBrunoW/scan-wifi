from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field, field_validator

Posture = Literal["standing", "sitting", "lying"]
Profile = Literal["adult", "child", "pet"]

_POSTURE_ALIASES = {
    "standing": "standing",
    "em_pe": "standing",
    "empe": "standing",
    "stand": "standing",
    "sitting": "sitting",
    "seated": "sitting",
    "sentado": "sitting",
    "sit": "sitting",
    "lying": "lying",
    "deitado": "lying",
    "lie": "lying",
}

_PROFILE_ALIASES = {
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


def normalize_posture(value: str) -> Posture:
    key = value.strip().lower().replace(" ", "_").replace("-", "_")
    if key not in _POSTURE_ALIASES:
        raise ValueError(f"postura invalida: {value}")
    return _POSTURE_ALIASES[key]  # type: ignore[return-value]


def normalize_profile(value: str) -> Profile:
    key = value.strip().lower().replace(" ", "_").replace("-", "_")
    if key not in _PROFILE_ALIASES:
        raise ValueError(f"perfil invalido: {value}")
    return _PROFILE_ALIASES[key]  # type: ignore[return-value]


class CSIPacketIn(BaseModel):
    seq: int = 0
    amp: list[float] = Field(default_factory=list)
    phase: list[float] = Field(default_factory=list)
    node_id: str = ""
    ts_ms: int = 0


class RoomIn(BaseModel):
    width: float = 8.0
    depth: float = 12.0
    esp_x: float = -2.0
    esp_z: float = -5.0
    esp2_x: float = 2.0
    esp2_z: float = -5.0
    tx_x: float = 0.0
    tx_z: float = 5.0
    link_m: float = 10.0


class PredictRequest(BaseModel):
    packets: list[CSIPacketIn] = Field(default_factory=list, min_length=1)
    packets2: list[CSIPacketIn] = Field(default_factory=list)
    stereo: bool = False
    room: RoomIn = Field(default_factory=RoomIn)


class PersonOut(BaseModel):
    id: int
    posture: Posture
    profile: Profile = "adult"
    x: float
    y: float
    z: float
    confidence: float


class PredictResponse(BaseModel):
    persons: list[PersonOut]
    calibrated: bool
    source: str
    device: str


class CalibrateRequest(BaseModel):
    posture: str
    packets: list[CSIPacketIn] = Field(default_factory=list)

    @field_validator("posture")
    @classmethod
    def _posture(cls, v: str) -> str:
        return normalize_posture(v)


class CalibrateResponse(BaseModel):
    posture: Posture
    samples: int
    ready: bool
    profiles: dict[str, int]


class CalibrateStatus(BaseModel):
    ready: bool
    profiles: dict[str, int]
    needed: int
    device: str


class MapCalibrateRequest(BaseModel):
    packets: list[CSIPacketIn] = Field(default_factory=list, min_length=1)
    x: float
    z: float
    y: float = 0.0
    label: str = ""


class CollectStart(BaseModel):
    posture: str
    profile: str = "adult"
    x: float | None = None
    y: float | None = None
    z: float | None = None


class CollectSave(BaseModel):
    posture: str | None = None
    profile: str | None = None
    packets: list[CSIPacketIn] = Field(default_factory=list)
    room: RoomIn | None = None
    x: float | None = None
    y: float | None = None
    z: float | None = None


class CalibrationMarkIn(BaseModel):
    label: str = Field(min_length=1)
    x: float
    z: float


class CalibrationSessionCreate(BaseModel):
    marks: list[CalibrationMarkIn] = Field(min_length=1)
    room: dict[str, float] = Field(default_factory=dict)


class CalibrationMarkStart(BaseModel):
    posture: str


class CalibrationPackets(BaseModel):
    packets: list[CSIPacketIn] = Field(default_factory=list)
