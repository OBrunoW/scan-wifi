"""Estabilizacao anti-drift: EMA, dead-zone 3cm e histerese postural/perfil."""
from __future__ import annotations

from dataclasses import dataclass, field


@dataclass
class TrackState:
    x: float = 0.0
    y: float = 0.0
    z: float = 0.0
    posture: str = "standing"
    profile: str = "adult"
    pose_votes: dict[str, int] = field(default_factory=dict)
    pending_pose: str | None = None
    profile_votes: dict[str, int] = field(default_factory=dict)
    pending_profile: str | None = None
    initialized: bool = False


class Stabilizer:
    """EMA + dead-zone 3cm + histerese de postura/perfil."""

    def __init__(
        self,
        alpha: float = 0.25,
        dead_zone_m: float = 0.03,
        pose_confirm: int = 4,
        profile_confirm: int = 6,
    ) -> None:
        self.alpha = alpha
        self.dead_zone_m = dead_zone_m
        self.pose_confirm = max(1, pose_confirm)
        self.profile_confirm = max(1, profile_confirm)
        self.tracks: dict[int, TrackState] = {}

    def reset(self) -> None:
        self.tracks.clear()

    def _track(self, pid: int) -> TrackState:
        if pid not in self.tracks:
            self.tracks[pid] = TrackState()
        return self.tracks[pid]

    def _hysteresis(
        self,
        current: str,
        pending: str | None,
        votes: dict[str, int],
        candidate: str,
        need: int,
    ) -> tuple[str, str | None, dict[str, int]]:
        if candidate == current:
            return current, candidate, {candidate: need}
        if pending != candidate:
            return current, candidate, {candidate: 1}
        votes = dict(votes)
        votes[candidate] = votes.get(candidate, 0) + 1
        if votes.get(candidate, 0) >= need:
            return candidate, candidate, {candidate: need}
        return current, pending, votes

    def stabilize_pose(self, pid: int, pose: str) -> str:
        tr = self._track(pid)
        if not tr.initialized:
            tr.posture = pose
            tr.pending_pose = pose
            tr.pose_votes = {pose: self.pose_confirm}
            return pose
        tr.posture, tr.pending_pose, tr.pose_votes = self._hysteresis(
            tr.posture, tr.pending_pose, tr.pose_votes, pose, self.pose_confirm
        )
        return tr.posture

    def stabilize_profile(self, pid: int, profile: str) -> str:
        tr = self._track(pid)
        if not tr.initialized:
            tr.profile = profile
            tr.pending_profile = profile
            tr.profile_votes = {profile: self.profile_confirm}
            return profile
        tr.profile, tr.pending_profile, tr.profile_votes = self._hysteresis(
            tr.profile, tr.pending_profile, tr.profile_votes, profile, self.profile_confirm
        )
        return tr.profile

    def stabilize_xyz(
        self,
        pid: int,
        x: float,
        y: float,
        z: float,
    ) -> tuple[float, float, float]:
        tr = self._track(pid)
        if not tr.initialized:
            tr.x, tr.y, tr.z = x, y, z
            tr.initialized = True
            return x, y, z

        dx = x - tr.x
        dy = y - tr.y
        dz = z - tr.z
        dist = (dx * dx + dy * dy + dz * dz) ** 0.5
        if dist < self.dead_zone_m:
            return tr.x, tr.y, tr.z

        a = self.alpha
        tr.x = a * x + (1 - a) * tr.x
        tr.y = a * y + (1 - a) * tr.y
        tr.z = a * z + (1 - a) * tr.z
        return tr.x, tr.y, tr.z

    def apply(
        self,
        pid: int,
        posture: str,
        x: float,
        y: float,
        z: float,
        profile: str = "adult",
    ) -> tuple[str, float, float, float, str]:
        pose = self.stabilize_pose(pid, posture)
        prof = self.stabilize_profile(pid, profile)
        if pose == "lying":
            y = min(y, 0.22)
            y = 0.2 if y > 0.05 else y
        elif pose == "sitting":
            y = 0.35 if abs(y) < 0.05 else y
        sx, sy, sz = self.stabilize_xyz(pid, x, y, z)
        if pose == "lying":
            sy = min(sy, 0.22)
        return pose, sx, sy, sz, prof


stabilizer = Stabilizer()
