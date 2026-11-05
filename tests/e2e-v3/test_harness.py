"""Prevent stale terminal state from satisfying a newer operation's assertion."""
import unittest
from run import Cycle, require_up_objective
from matrix import Matrix


class CompletionTests(unittest.TestCase):
    def test_rolling_up_objective_uses_episode_start_and_exact_boundary(self):
        for seconds in (119.999, 120, 120.001):
            with self.subTest(seconds=seconds):
                import datetime as dt
                start = dt.datetime(2026, 10, 30, tzinfo=dt.timezone.utc)
                operation = {"episodeStart": start.isoformat(),
                    "started": (start + dt.timedelta(seconds=30)).isoformat(),
                    "completedAt": (start + dt.timedelta(seconds=seconds)).isoformat(),
                    "deadlineMissed": False}
                if seconds <= 120:
                    self.assertEqual(require_up_objective(operation), seconds)
                else:
                    with self.assertRaisesRegex(RuntimeError, "120-second"):
                        require_up_objective(operation)

    def test_deadline_miss_cannot_be_hidden_by_a_shorter_terminal_duration(self):
        with self.assertRaisesRegex(RuntimeError, "120-second"):
            require_up_objective({"episodeStart": "2026-10-30T00:00:00Z",
                "completedAt": "2026-10-30T00:01:00Z", "deadlineMissed": True})

    def test_observer_wall_time_cannot_hide_a_late_confirmation(self):
        operation = {"episodeStart": "2026-10-30T00:00:00Z",
            "completedAt": "2026-10-30T00:01:59Z", "deadlineMissed": False}
        self.assertEqual(require_up_objective(operation, "2026-10-30T00:02:00Z"), 119)
        with self.assertRaisesRegex(RuntimeError, "120-second"):
            require_up_objective(operation, "2026-10-30T00:02:00.001Z")

    def test_previous_down_completion_does_not_finish_new_active_down(self):
        cycle = Cycle.__new__(Cycle)
        cycle.observe = lambda: {"operation": {"revision": 9},
            "lastOperation": {"revision": 8, "direction": "down", "stage": "Completed"}}
        self.assertFalse(cycle.completed("down"))

    def test_actual_terminal_direction_completes(self):
        cycle = Cycle.__new__(Cycle)
        cycle.observe = lambda: {"lastOperation": {"direction": "down", "stage": "Completed"}}
        self.assertTrue(cycle.completed("down"))
        self.assertFalse(cycle.completed("up"))

    def test_rolling_availability_failure_is_immediate_before_completion(self):
        matrix = Matrix.__new__(Matrix)
        matrix.completed = lambda direction: self.fail("must sample availability before completion")
        def unavailable():
            raise RuntimeError("availability dropped")
        matrix.rolling_availability = unavailable
        with self.assertRaisesRegex(RuntimeError, "availability dropped"):
            matrix.wait_rolling("up", {}, timeout=60)


if __name__ == "__main__":
    unittest.main()
