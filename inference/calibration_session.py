from __future__ import annotations

import json
import logging
import os
import threading
import time
from dataclasses import dataclass, replace
from pathlib import Path
from typing import Callable

from calibration import (
    GOAL_WINDOWS,
    MIN_COMPLETE_MARKS,
    POSTURES,
    CalibrationError,
    Window,
    build_calibration_payload,
    ensure_enough_marks,
    flag_outliers,
    lomo_error,
    marks_from_windows,
    posture_profiles,
)
from calibration_store import CALIBRATION_DIR, save_calibration, write_json_atomic
from collector import Collector
from features import signature_vector
from schemas import CSIPacketIn, normalize_posture

logger = logging.getLogger(__name__)

WINDOW_PACKETS = int(os.getenv("CALIBRATE_PACKETS", "50"))
NO_CSI_SECONDS = 3.0
EXPIRE_SECONDS = 30 * 60
POSTURE_HEIGHT_M = {"standing": 0.9, "sitting": 0.35, "lying": 0.2}

STATE_EMPTY = "vazia"
STATE_COLLECTING = "coletando"
STATE_COLLECTING_MARK = "coletando_marco"
STATE_NO_CSI = "sem_csi"
STATE_MARK_COMPLETE = "marco_completo"

MARK_PENDING = "pendente"
MARK_COLLECTING = "coletando"
MARK_COMPLETE = "completo"
MARK_SKIPPED = "pulado"


@dataclass
class WindowRecord:
    window: Window
    files: list[str]


@dataclass
class MarkState:
    label: str
    x: float
    z: float
    status: str = MARK_PENDING


class CalibrationSession:
    def __init__(
        self,
        marks: list[dict],
        room: dict,
        clock: Callable[[], float] = time.time,
        root: Path = CALIBRATION_DIR,
        goal: int = GOAL_WINDOWS,
        window_packets: int = WINDOW_PACKETS,
    ) -> None:
        if not marks:
            raise CalibrationError("BAD_REQUEST", "informe os marcos da sala")
        self.clock = clock
        self.goal = goal
        self.window_packets = window_packets
        self.room = room
        self.created_at = clock()
        self.id = "cal-" + time.strftime("%Y%m%d-%H%M%S", time.localtime(self.created_at))
        self.directory = root / self.id
        self.root = root
        self.marks: dict[str, MarkState] = {}
        for mark in marks:
            label = str(mark["label"])
            if label in self.marks:
                raise CalibrationError("BAD_REQUEST", f"marco duplicado: {label}")
            self.marks[label] = MarkState(label, float(mark["x"]), float(mark["z"]))
        self.records: list[WindowRecord] = []
        self.collector = Collector()
        self.active_label: str | None = None
        self.active_posture: str | None = None
        self.pending: list[CSIPacketIn] = []
        self.last_packet_at = 0.0
        self.last_activity = self.created_at
        self.state = STATE_COLLECTING

    def touch(self) -> None:
        self.last_activity = self.clock()

    def expired(self) -> bool:
        return self.clock() - self.last_activity > EXPIRE_SECONDS

    def mark(self, label: str) -> MarkState:
        state = self.marks.get(label)
        if state is None:
            raise CalibrationError("BAD_REQUEST", f"marco desconhecido: {label}")
        return state

    def count(self, label: str, posture: str) -> int:
        return sum(1 for r in self.records if r.window.label == label and r.window.posture == posture)

    def start_mark(self, label: str, posture: str) -> dict:
        self.touch()
        mark = self.mark(label)
        try:
            pose = normalize_posture(posture)
        except ValueError as error:
            raise CalibrationError("BAD_REQUEST", str(error)) from error
        if mark.status == MARK_SKIPPED:
            raise CalibrationError("BAD_REQUEST", f"marco {label} foi pulado")
        if self.count(label, pose) >= self.goal:
            raise CalibrationError("BAD_REQUEST", f"{label} ja tem {self.goal} janelas em {pose}; refaca o marco")
        self.release_active()
        mark.status = MARK_COLLECTING
        self.active_label = label
        self.active_posture = pose
        self.pending = []
        self.last_packet_at = self.clock()
        self.state = STATE_COLLECTING_MARK
        return self.snapshot()

    def release_active(self) -> None:
        if self.active_label is not None:
            previous = self.marks[self.active_label]
            if previous.status == MARK_COLLECTING:
                previous.status = MARK_PENDING
        self.active_label = None
        self.active_posture = None
        self.pending = []

    def push_packets(self, label: str, packets: list[CSIPacketIn]) -> dict:
        self.touch()
        self.mark(label)
        if self.active_label != label or self.active_posture is None:
            raise CalibrationError("BAD_REQUEST", f"marco {label} nao esta em coleta", status=409)
        if not packets:
            raise CalibrationError("BAD_REQUEST", "packets vazio")
        now = self.clock()
        if now - self.last_packet_at > NO_CSI_SECONDS:
            self.pending = []
        self.last_packet_at = now
        self.state = STATE_COLLECTING_MARK
        self.pending.extend(packets)
        while len(self.pending) >= self.window_packets and self.active_label is not None:
            window_packets = self.pending[: self.window_packets]
            self.pending = self.pending[self.window_packets :]
            self.save_window(label, self.active_posture, window_packets)
            if self.count(label, self.active_posture) >= self.goal:
                self.complete_active()
        return self.snapshot()

    def save_window(self, label: str, posture: str, packets: list[CSIPacketIn]) -> None:
        mark = self.marks[label]
        index = self.count(label, posture)
        try:
            signature = signature_vector(packets)
            self.collector.start(posture, x=mark.x, y=POSTURE_HEIGHT_M[posture], z=mark.z, out_dir=self.directory)
            saved = self.collector.save(
                packets,
                meta={"label": label, "calibration_id": self.id, "window_index": index, "discarded": False},
                out_dir=self.directory,
            )
        except ValueError as error:
            self.collector.active = None
            self.collector.buffer = []
            raise CalibrationError("BAD_REQUEST", str(error)) from error
        window = Window(label=label, x=mark.x, z=mark.z, posture=posture, signature=signature)
        self.records.append(WindowRecord(window=window, files=saved["files"]))

    def complete_active(self) -> None:
        if self.active_label is not None:
            self.marks[self.active_label].status = MARK_COMPLETE
        self.active_label = None
        self.active_posture = None
        self.pending = []
        self.state = STATE_MARK_COMPLETE

    def skip_mark(self, label: str) -> dict:
        self.touch()
        mark = self.mark(label)
        self.remove_samples(label)
        mark.status = MARK_SKIPPED
        self.state = STATE_COLLECTING
        return self.snapshot()

    def clear_samples(self, label: str) -> dict:
        self.touch()
        mark = self.mark(label)
        self.remove_samples(label)
        mark.status = MARK_PENDING
        self.state = STATE_COLLECTING
        return self.snapshot()

    def remove_samples(self, label: str) -> None:
        if self.active_label == label:
            self.active_label = None
            self.active_posture = None
            self.pending = []
        kept: list[WindowRecord] = []
        for record in self.records:
            if record.window.label == label:
                self.delete_files(record)
            else:
                kept.append(record)
        self.records = kept

    def delete_files(self, record: WindowRecord) -> None:
        for name in record.files:
            (self.directory / name).unlink(missing_ok=True)

    def discard_all(self) -> None:
        for record in self.records:
            self.delete_files(record)
        self.records = []
        if self.directory.exists() and not any(self.directory.iterdir()):
            self.directory.rmdir()

    def current_state(self) -> str:
        if self.state == STATE_COLLECTING_MARK and self.clock() - self.last_packet_at > NO_CSI_SECONDS:
            return STATE_NO_CSI
        return self.state

    def snapshot(self) -> dict:
        rows = []
        for mark in self.marks.values():
            rows.append(
                {
                    "label": mark.label,
                    "x": mark.x,
                    "z": mark.z,
                    "status": mark.status,
                    "samples": {p: self.count(mark.label, p) for p in POSTURES},
                }
            )
        return {
            "id": self.id,
            "state": self.current_state(),
            "goal": self.goal,
            "window_packets": self.window_packets,
            "active": (
                {"label": self.active_label, "posture": self.active_posture, "buffered": len(self.pending)}
                if self.active_label
                else None
            ),
            "marks": rows,
            "complete_marks": sum(1 for m in self.marks.values() if m.status == MARK_COMPLETE),
            "no_csi_after_s": NO_CSI_SECONDS,
        }

    def mark_discarded_in_files(self, record: WindowRecord) -> None:
        meta_path = self.directory / record.files[2]
        try:
            payload = json.loads(meta_path.read_text())
        except (OSError, json.JSONDecodeError) as error:
            raise CalibrationError("CALIBRATION_CORRUPT", f"amostra ilegivel: {meta_path.name}: {error}", status=500) from error
        payload.setdefault("meta", {})["discarded"] = True
        write_json_atomic(meta_path, payload)

    def compute(self) -> dict:
        self.touch()
        if not self.records:
            raise CalibrationError("CALIBRATION_NO_CSI", "nenhuma janela CSI coletada", status=409)
        completed = sum(1 for m in self.marks.values() if m.status == MARK_COMPLETE)
        if completed < MIN_COMPLETE_MARKS:
            raise CalibrationError(
                "CALIBRATION_TOO_FEW_MARKS",
                f"{completed} marcos completos; minimo {MIN_COMPLETE_MARKS}",
                status=409,
            )
        flagged = flag_outliers([r.window for r in self.records])
        for record, window in zip(self.records, flagged):
            if window.discarded and not record.window.discarded:
                self.mark_discarded_in_files(record)
            record.window = replace(record.window, discarded=window.discarded)
        marks = marks_from_windows(flagged, self.goal)
        ensure_enough_marks(marks)
        report = lomo_error(marks)
        profiles = posture_profiles(marks)
        definitions = [{"label": m.label, "x": m.x, "z": m.z} for m in self.marks.values()]
        skipped = {m.label for m in self.marks.values() if m.status == MARK_SKIPPED}
        payload = build_calibration_payload(
            self.id,
            int(self.created_at * 1000),
            self.room,
            definitions,
            skipped,
            marks,
            report,
            profiles,
        )
        save_calibration(payload, self.root)
        self.state = "calculada"
        return payload


class GuidedCalibration:
    def __init__(self, clock: Callable[[], float] = time.time, root: Path = CALIBRATION_DIR) -> None:
        self.clock = clock
        self.root = root
        self.session: CalibrationSession | None = None
        self.lock = threading.RLock()

    def create(self, marks: list[dict], room: dict) -> dict:
        with self.lock:
            if self.session is not None:
                self.session.discard_all()
            self.session = CalibrationSession(marks, room, clock=self.clock, root=self.root)
            return self.session.snapshot()

    def current(self) -> CalibrationSession:
        if self.session is None:
            raise CalibrationError("CALIBRATION_NO_SESSION", "nenhuma sessao de calibracao ativa", status=409)
        if self.session.expired():
            logger.warning("calibration session %s expired; discarding samples", self.session.id)
            self.session.discard_all()
            self.session = None
            raise CalibrationError("CALIBRATION_NO_SESSION", "sessao de calibracao expirou", status=409)
        return self.session

    def snapshot(self) -> dict:
        with self.lock:
            try:
                return self.current().snapshot()
            except CalibrationError as error:
                if error.code != "CALIBRATION_NO_SESSION":
                    raise
                return {"state": STATE_EMPTY}

    def start_mark(self, label: str, posture: str) -> dict:
        with self.lock:
            return self.current().start_mark(label, posture)

    def push_packets(self, label: str, packets: list[CSIPacketIn]) -> dict:
        with self.lock:
            return self.current().push_packets(label, packets)

    def skip_mark(self, label: str) -> dict:
        with self.lock:
            return self.current().skip_mark(label)

    def clear_samples(self, label: str) -> dict:
        with self.lock:
            return self.current().clear_samples(label)

    def compute(self) -> dict:
        with self.lock:
            payload = self.current().compute()
            self.session = None
            return payload


guided = GuidedCalibration()
