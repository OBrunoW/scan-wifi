import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import numpy as np

import calibration
import calibration_store
import collector as collector_module
from calibration import CalibrationError, Window
from calibration_session import CalibrationSession, GuidedCalibration
from collector import Collector
from mapping import ActiveCalibration, SpatialMapper
from schemas import CSIPacketIn

SQUARE = [("M1", 0.0, 0.0), ("M2", 2.0, 0.0), ("M3", 0.0, 2.0), ("M4", 2.0, 2.0)]


def window(label, x, z, posture, vector):
    return Window(label=label, x=x, z=z, posture=posture, signature=np.asarray(vector, dtype=np.float32))


def packets(count, level=100.0, subcarriers=16):
    return [CSIPacketIn(seq=i, amp=[level + (i % 3)] * subcarriers, phase=[10.0] * subcarriers) for i in range(count)]


def identical_windows(posture="standing", per_mark=3):
    items = []
    for label, x, z in SQUARE:
        for _ in range(per_mark):
            items.append(window(label, x, z, posture, [1.0, 1.0, 1.0, 1.0]))
    return items


class FakeClock:
    def __init__(self):
        self.now = 1_700_000_000.0

    def __call__(self):
        return self.now

    def advance(self, seconds):
        self.now += seconds


class CollectorCompatibilityTest(unittest.TestCase):
    def test_save_without_directory_uses_data_dir(self):
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.object(collector_module, "DATA_DIR", Path(tmp)):
                instance = Collector()
                instance.start("standing")
                result = instance.save(packets(10))
                self.assertTrue((Path(tmp) / result["files"][2]).exists())

    def test_save_with_directory_uses_it(self):
        with tempfile.TemporaryDirectory() as tmp, tempfile.TemporaryDirectory() as other:
            with mock.patch.object(collector_module, "DATA_DIR", Path(other)):
                instance = Collector()
                target = Path(tmp) / "nested"
                instance.start("sitting", out_dir=target)
                result = instance.save(packets(10))
                self.assertTrue((target / result["files"][2]).exists())
                self.assertEqual(list(Path(other).glob("*.json")), [])


class AggregationTest(unittest.TestCase):
    def test_counts_and_mean_per_mark(self):
        windows = [
            window("M1", 0, 0, "standing", [1, 1]),
            window("M1", 0, 0, "standing", [3, 3]),
            window("M1", 0, 0, "sitting", [10, 10]),
        ]
        marks = calibration.marks_from_windows(windows, goal=2)
        mark = marks["M1"]
        self.assertEqual(mark.collected["standing"], 2)
        self.assertEqual(mark.collected["sitting"], 1)
        np.testing.assert_allclose(mark.posture_means["standing"], [2, 2])
        np.testing.assert_allclose(mark.signature, [6, 6])
        self.assertTrue(mark.complete)

    def test_mark_below_goal_is_not_complete(self):
        marks = calibration.marks_from_windows([window("M1", 0, 0, "standing", [1])], goal=2)
        self.assertFalse(marks["M1"].complete)


class OutlierTest(unittest.TestCase):
    def test_far_window_is_discarded(self):
        rng = np.random.default_rng(0)
        windows = [window("M1", 0, 0, "standing", rng.normal(0, 0.01, 4)) for _ in range(14)]
        windows.append(window("M1", 0, 0, "standing", [5, 5, 5, 5]))
        flagged = calibration.flag_outliers(windows)
        self.assertEqual([w.discarded for w in flagged].count(True), 1)
        self.assertTrue(flagged[-1].discarded)

    def test_fewer_than_five_windows_discard_nothing(self):
        windows = [window("M1", 0, 0, "standing", [0, 0]) for _ in range(3)]
        windows.append(window("M1", 0, 0, "standing", [9, 9]))
        self.assertFalse(any(w.discarded for w in calibration.flag_outliers(windows)))

    def test_discarded_window_leaves_the_mean(self):
        rng = np.random.default_rng(1)
        windows = [window("M1", 0, 0, "standing", rng.normal(0, 0.01, 2)) for _ in range(14)]
        windows.append(window("M1", 0, 0, "standing", [50, 50]))
        marks = calibration.marks_from_windows(calibration.flag_outliers(windows), goal=15)
        self.assertEqual(marks["M1"].accepted["standing"], 14)
        self.assertLess(abs(float(marks["M1"].signature[0])), 0.1)
        self.assertTrue(marks["M1"].complete)


class ProfilesTest(unittest.TestCase):
    def test_posture_needs_two_marks(self):
        windows = [
            window("M1", 0, 0, "standing", [1]),
            window("M2", 1, 0, "standing", [3]),
            window("M1", 0, 0, "sitting", [5]),
        ]
        marks = calibration.marks_from_windows(windows, goal=1)
        profiles = calibration.posture_profiles(marks)
        self.assertIn("standing", profiles)
        self.assertNotIn("sitting", profiles)
        np.testing.assert_allclose(profiles["standing"], [2])
        self.assertFalse(calibration.profiles_ready(profiles))

    def test_ready_when_all_postures_in_two_marks(self):
        windows = []
        for posture in calibration.POSTURES:
            windows.append(window("M1", 0, 0, posture, [1]))
            windows.append(window("M2", 1, 0, posture, [2]))
        profiles = calibration.posture_profiles(calibration.marks_from_windows(windows, goal=1))
        self.assertTrue(calibration.profiles_ready(profiles))


class LomoTest(unittest.TestCase):
    def test_known_square_error(self):
        marks = calibration.marks_from_windows(identical_windows(), goal=3)
        report = calibration.lomo_error(marks)
        expected = (4.0 / 3.0) * np.sqrt(2.0)
        self.assertAlmostEqual(report.mean_m, expected, places=4)
        self.assertAlmostEqual(report.p90_m, expected, places=4)
        self.assertEqual(report.windows, 12)
        self.assertEqual(report.marks, 4)
        for value in report.per_mark.values():
            self.assertAlmostEqual(value, expected, places=4)

    def test_distinct_signatures_recover_position(self):
        windows = []
        for label, x, z in SQUARE:
            vector = [x * 10.0, z * 10.0, 1.0, 1.0]
            for _ in range(3):
                windows.append(window(label, x, z, "standing", vector))
        report = calibration.lomo_error(calibration.marks_from_windows(windows, goal=3))
        self.assertLess(report.mean_m, 2.5)

    def test_too_few_marks_for_lomo(self):
        marks = calibration.marks_from_windows(identical_windows()[:6], goal=3)
        with self.assertRaises(CalibrationError):
            calibration.lomo_error(marks)

    def test_ensure_enough_marks_error_code(self):
        marks = calibration.marks_from_windows(identical_windows()[:9], goal=3)
        with self.assertRaises(CalibrationError) as ctx:
            calibration.ensure_enough_marks(marks)
        self.assertEqual(ctx.exception.code, "CALIBRATION_TOO_FEW_MARKS")

    def test_baseline_and_improvement(self):
        marks = calibration.marks_from_windows(identical_windows(), goal=3)
        baseline = calibration.baseline_error(marks, {"M1": [(3.0, 4.0)], "M2": [(2.0, 1.0)]})
        self.assertAlmostEqual(baseline["mean_m"], 3.0)
        self.assertEqual(baseline["samples"], 2)
        self.assertAlmostEqual(calibration.improvement_ratio(1.5, 3.0), 0.5)
        self.assertIsNone(calibration.improvement_ratio(1.0, 0.0))


class PersistenceTest(unittest.TestCase):
    def test_active_missing_returns_none(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIsNone(calibration_store.read_active_id(Path(tmp)))

    def test_active_corrupt_raises_and_startup_survives(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "active.json").write_text("{broken")
            with self.assertRaises(CalibrationError):
                calibration_store.read_active_id(root)
            mapper = SpatialMapper(path=root / "map.json")
            with self.assertLogs("calibration_store", level="ERROR"):
                self.assertIsNone(calibration_store.load_active_on_startup(mapper, root))
            self.assertIsNone(mapper.guided)

    def test_atomic_write_leaves_no_temp_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = Path(tmp) / "a" / "file.json"
            calibration_store.write_json_atomic(target, {"a": 1})
            calibration_store.write_json_atomic(target, {"a": 2})
            self.assertEqual(json.loads(target.read_text()), {"a": 2})
            self.assertEqual([p.name for p in target.parent.iterdir()], ["file.json"])

    def test_unknown_id_is_not_found(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(CalibrationError) as ctx:
                calibration_store.load_calibration("cal-x", Path(tmp))
            self.assertEqual(ctx.exception.code, "CALIBRATION_NOT_FOUND")

    def test_path_traversal_id_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(CalibrationError) as ctx:
                calibration_store.load_calibration("../x", Path(tmp))
            self.assertEqual(ctx.exception.code, "BAD_REQUEST")


def payload_for(calibration_id, created_at, anchors):
    return {
        "id": calibration_id,
        "created_at": created_at,
        "anchors": anchors,
        "posture_profiles": {},
        "lomo": {"mean_m": 0.5, "p90_m": 0.9, "windows": 4},
        "marks": [],
        "status": "calculada",
    }


class ActivationTest(unittest.TestCase):
    def anchors(self, offset):
        return [
            {"label": "A", "x": 0.0 + offset, "z": 0.0, "y": 0.0, "signature": [0.0, 0.0]},
            {"label": "B", "x": 4.0 + offset, "z": 0.0, "y": 0.0, "signature": [10.0, 10.0]},
        ]

    def test_activate_swaps_and_survives_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calibration_store.save_calibration(payload_for("cal-1", 1, self.anchors(0.0)), root)
            calibration_store.save_calibration(payload_for("cal-2", 2, self.anchors(1.0)), root)
            mapper = SpatialMapper(path=root / "map.json")
            self.assertFalse(mapper.ready())
            calibration_store.activate("cal-1", mapper, root)
            self.assertTrue(mapper.ready())
            self.assertEqual(mapper.guided.id, "cal-1")
            calibration_store.activate("cal-2", mapper, root)
            self.assertEqual(mapper.guided.id, "cal-2")
            self.assertEqual(calibration_store.load_calibration("cal-1", root)["status"], "inativa")
            self.assertEqual(calibration_store.load_calibration("cal-2", root)["status"], "ativa")
            restarted = SpatialMapper(path=root / "map.json")
            loaded = calibration_store.load_active_on_startup(restarted, root)
            self.assertEqual(loaded.id, "cal-2")
            self.assertTrue(restarted.ready())

    def test_activation_reports_position_jump(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calibration_store.save_calibration(payload_for("cal-1", 1, self.anchors(0.0)), root)
            calibration_store.save_calibration(payload_for("cal-2", 2, self.anchors(2.0)), root)
            mapper = SpatialMapper(path=root / "map.json")
            _, first_jump = calibration_store.activate("cal-1", mapper, root)
            self.assertIsNone(first_jump)
            mapper.map_xz(packets(5))
            _, second_jump = calibration_store.activate("cal-2", mapper, root)
            self.assertAlmostEqual(second_jump, 2.0, places=3)
            self.assertGreater(second_jump, calibration_store.POSITION_JUMP_WARNING_M)

    def test_activation_without_traffic_has_no_jump(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calibration_store.save_calibration(payload_for("cal-1", 1, self.anchors(0.0)), root)
            calibration_store.save_calibration(payload_for("cal-2", 2, self.anchors(2.0)), root)
            mapper = SpatialMapper(path=root / "map.json")
            calibration_store.activate("cal-1", mapper, root)
            _, jump = calibration_store.activate("cal-2", mapper, root)
            self.assertIsNone(jump)

    def test_guided_anchors_replace_manual_ones(self):
        with tempfile.TemporaryDirectory() as tmp:
            mapper = SpatialMapper(path=Path(tmp) / "map.json")
            mapper.anchors = [{"label": "m", "x": 9.0, "z": 9.0, "signature": [0.0]}]
            mapper.set_guided(ActiveCalibration(id="g", anchors=self.anchors(0.0)))
            self.assertEqual(mapper.effective_anchors()[0]["label"], "A")
            mapper.set_guided(None)
            self.assertEqual(mapper.effective_anchors()[0]["label"], "m")

    def test_activate_unknown_id(self):
        with tempfile.TemporaryDirectory() as tmp:
            mapper = SpatialMapper(path=Path(tmp) / "map.json")
            with self.assertRaises(CalibrationError) as ctx:
                calibration_store.activate("nope", mapper, Path(tmp))
            self.assertEqual(ctx.exception.code, "CALIBRATION_NOT_FOUND")

    def test_cannot_discard_active(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calibration_store.save_calibration(payload_for("cal-1", 1, self.anchors(0.0)), root)
            mapper = SpatialMapper(path=root / "map.json")
            calibration_store.activate("cal-1", mapper, root)
            with self.assertRaises(CalibrationError):
                calibration_store.discard("cal-1", mapper, root)


MARKS = [{"label": label, "x": x, "z": z} for label, x, z in SQUARE + [("M5", 1.0, 1.0), ("M6", 3.0, 3.0)]]


class SessionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.clock = FakeClock()
        self.root = Path(self.tmp.name)

    def session(self, goal=2):
        return CalibrationSession(MARKS, {"width": 8.0, "depth": 12.0}, clock=self.clock, root=self.root, goal=goal, window_packets=10)

    def fill(self, session, label, posture="standing", level=100.0):
        session.start_mark(label, posture)
        for _ in range(session.goal):
            session.push_packets(label, packets(10, level=level))

    def test_mark_completes_at_goal(self):
        session = self.session(goal=3)
        session.start_mark("M1", "standing")
        for expected in (1, 2):
            state = session.push_packets("M1", packets(10))
            self.assertEqual(state["marks"][0]["samples"]["standing"], expected)
            self.assertEqual(state["marks"][0]["status"], "coletando")
        state = session.push_packets("M1", packets(10))
        self.assertEqual(state["marks"][0]["status"], "completo")
        self.assertEqual(state["state"], "marco_completo")
        self.assertEqual(len(list((self.root / session.id).glob("*.json"))), 3)

    def test_window_files_carry_label_and_calibration_id(self):
        session = self.session()
        session.start_mark("M2", "lying")
        session.push_packets("M2", packets(10))
        meta_file = next((self.root / session.id).glob("lying_*.json"))
        payload = json.loads(meta_file.read_text())
        self.assertEqual(payload["meta"]["label"], "M2")
        self.assertEqual(payload["meta"]["calibration_id"], session.id)
        self.assertEqual(payload["xyz"]["x"], 2.0)

    def test_partial_window_is_not_saved(self):
        session = self.session()
        session.start_mark("M1", "standing")
        state = session.push_packets("M1", packets(7))
        self.assertEqual(state["marks"][0]["samples"]["standing"], 0)
        self.assertEqual(state["active"]["buffered"], 7)
        self.assertEqual(list((self.root / session.id).glob("*")) if (self.root / session.id).exists() else [], [])

    def test_redo_removes_only_that_mark(self):
        session = self.session()
        self.fill(session, "M1")
        self.fill(session, "M2")
        session.clear_samples("M1")
        state = session.snapshot()
        counts = {m["label"]: m["samples"]["standing"] for m in state["marks"]}
        self.assertEqual(counts["M1"], 0)
        self.assertEqual(counts["M2"], 2)
        self.assertEqual(len(list((self.root / session.id).glob("*.json"))), 2)
        self.assertEqual(state["marks"][0]["status"], "pendente")

    def test_skip_records_no_samples(self):
        session = self.session()
        state = session.skip_mark("M3")
        row = next(m for m in state["marks"] if m["label"] == "M3")
        self.assertEqual(row["status"], "pulado")
        self.assertEqual(sum(row["samples"].values()), 0)
        with self.assertRaises(CalibrationError):
            session.start_mark("M3", "standing")

    def test_silence_turns_state_into_no_csi_without_saving(self):
        session = self.session()
        session.start_mark("M1", "standing")
        self.clock.advance(2.0)
        self.assertEqual(session.snapshot()["state"], "coletando_marco")
        self.clock.advance(1.5)
        self.assertEqual(session.snapshot()["state"], "sem_csi")
        self.assertFalse((self.root / session.id).exists())

    def test_stale_buffer_is_dropped_after_silence(self):
        session = self.session()
        session.start_mark("M1", "standing")
        session.push_packets("M1", packets(6))
        self.clock.advance(5.0)
        state = session.push_packets("M1", packets(6))
        self.assertEqual(state["marks"][0]["samples"]["standing"], 0)
        self.assertEqual(state["state"], "coletando_marco")

    def test_unknown_mark_and_bad_posture(self):
        session = self.session()
        with self.assertRaises(CalibrationError):
            session.start_mark("M9", "standing")
        with self.assertRaises(CalibrationError):
            session.start_mark("M1", "flying")

    def test_push_requires_started_mark(self):
        session = self.session()
        with self.assertRaises(CalibrationError):
            session.push_packets("M1", packets(10))

    def test_compute_with_three_marks_is_too_few(self):
        session = self.session()
        for label in ("M1", "M2", "M3"):
            self.fill(session, label)
        with self.assertRaises(CalibrationError) as ctx:
            session.compute()
        self.assertEqual(ctx.exception.code, "CALIBRATION_TOO_FEW_MARKS")
        self.assertFalse((self.root / session.id / "calibration.json").exists())

    def test_compute_without_windows_reports_no_csi(self):
        with self.assertRaises(CalibrationError) as ctx:
            self.session().compute()
        self.assertEqual(ctx.exception.code, "CALIBRATION_NO_CSI")

    def test_compute_persists_calibration(self):
        session = self.session()
        for index, label in enumerate(("M1", "M2", "M3", "M4", "M5")):
            self.fill(session, label, level=60.0 + 20.0 * index)
        session.skip_mark("M6")
        payload = session.compute()
        stored = json.loads((self.root / session.id / "calibration.json").read_text())
        self.assertEqual(stored["id"], payload["id"])
        self.assertEqual(len(stored["anchors"]), 5)
        self.assertEqual(stored["lomo"]["marks"], 5)
        self.assertEqual(stored["status"], "calculada")
        skipped = {m["label"]: m["skipped"] for m in stored["marks"]}
        self.assertTrue(skipped["M6"])
        self.assertFalse(skipped["M1"])

    def test_session_expires_after_inactivity(self):
        manager = GuidedCalibration(clock=self.clock, root=self.root)
        manager.create(MARKS, {})
        manager.start_mark("M1", "standing")
        self.clock.advance(31 * 60)
        with self.assertRaises(CalibrationError) as ctx:
            manager.start_mark("M1", "standing")
        self.assertEqual(ctx.exception.code, "CALIBRATION_NO_SESSION")
        self.assertEqual(manager.snapshot()["state"], "vazia")

    def test_manager_without_session(self):
        manager = GuidedCalibration(clock=self.clock, root=self.root)
        with self.assertRaises(CalibrationError) as ctx:
            manager.skip_mark("M1")
        self.assertEqual(ctx.exception.code, "CALIBRATION_NO_SESSION")


if __name__ == "__main__":
    unittest.main()
