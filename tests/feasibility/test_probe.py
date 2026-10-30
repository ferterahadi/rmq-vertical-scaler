"""Guard against a false feasibility pass when Kubernetes status is incomplete."""

import copy
import unittest

import run as probe


def converged_pod():
    return {
        "metadata": {"generation": 2},
        "spec": {"containers": [{"name": "rabbitmq", "resources": copy.deepcopy(probe.LARGER)}]},
        "status": {"observedGeneration": 2, "conditions": [], "containerStatuses": [{
            "name": "rabbitmq", "ready": True, "resources": copy.deepcopy(probe.LARGER),
        }]},
    }


class ResourceStatusTests(unittest.TestCase):
    def test_complete_current_status_passes(self):
        self.assertTrue(probe.ready_at(converged_pod(), probe.LARGER))

    def test_missing_or_old_status_never_uses_spec_as_proof(self):
        for change in (lambda p: p["status"].pop("containerStatuses"),
                       lambda p: p["status"].update(observedGeneration=1),
                       lambda p: p["status"]["containerStatuses"][0].pop("resources"),
                       lambda p: p["status"]["containerStatuses"][0].update(resources=probe.BASELINE)):
            with self.subTest(change=change):
                pod = converged_pod()
                change(pod)
                self.assertFalse(probe.ready_at(pod, probe.LARGER))

    def test_pending_and_in_progress_never_pass(self):
        for condition in ("PodResizePending", "PodResizeInProgress"):
            pod = converged_pod()
            pod["status"]["conditions"] = [{"type": condition, "status": "True"}]
            self.assertFalse(probe.ready_at(pod, probe.LARGER))

    def test_unready_or_terminating_never_pass(self):
        pod = converged_pod()
        pod["status"]["containerStatuses"][0]["ready"] = False
        self.assertFalse(probe.ready_at(pod, probe.LARGER))
        pod = converged_pod()
        pod["metadata"]["deletionTimestamp"] = "2026-10-26T00:00:00Z"
        self.assertFalse(probe.ready_at(pod, probe.LARGER))

    def test_equal_requests_do_not_hide_different_limits(self):
        pod = converged_pod()
        pod["status"]["containerStatuses"][0]["resources"]["limits"]["memory"] = "1Gi"
        self.assertFalse(probe.ready_at(pod, probe.LARGER))

    def test_numeric_resource_equality(self):
        self.assertTrue(probe.equal_resources(
            {"limits": {"cpu": "1500m", "memory": "1572864Ki"}},
            {"limits": {"cpu": "1.5", "memory": "1536Mi"}}))
        self.assertFalse(probe.equal_resources({}, probe.BASELINE))


class CensusTests(unittest.TestCase):
    def test_missing_or_extra_brokers_never_pass(self):
        complete = {"brokers": [{"name": name} for name in probe.BROKER_NAMES]}
        for brokers in ([], complete["brokers"][:2], complete["brokers"] + [{"name": "foreign"}]):
            with self.subTest(brokers=brokers), self.assertRaises(RuntimeError):
                probe.broker_pairs(complete, {"brokers": brokers})

    def test_matches_by_name_instead_of_list_position(self):
        brokers = [{"name": name} for name in probe.BROKER_NAMES]
        pairs = probe.broker_pairs({"brokers": brokers}, {"brokers": list(reversed(brokers))})
        self.assertEqual(len(pairs), 3)
        self.assertTrue(all(a["name"] == b["name"] for a, b in pairs))

    def test_owner_must_be_controlling_uid(self):
        pod = {"metadata": {"ownerReferences": [{"kind": "StatefulSet", "uid": "ours", "controller": True}]}}
        self.assertTrue(probe.owns(pod, "StatefulSet", "ours"))
        self.assertFalse(probe.owns(pod, "StatefulSet", "other"))
        pod["metadata"]["ownerReferences"][0]["controller"] = False
        self.assertFalse(probe.owns(pod, "StatefulSet", "ours"))


class ConfigurationTests(unittest.TestCase):
    def test_requires_matching_configured_budget_not_just_direction(self):
        snapshot = {"operatorMemoryOverride": 322122548, "expectedMemoryOverride": 322122548, "brokers": [{
            "name": name, "relativeWatermark": "0.6", "watermarkBytes": 193273528,
            "mountedMemoryOverride": 322122548,
        } for name in probe.BROKER_NAMES]}
        self.assertTrue(probe.budget_matches(snapshot))
        for bad in (400000000, 515396077, 402653184, None):
            changed = copy.deepcopy(snapshot)
            if bad is None:
                changed["brokers"][0].pop("watermarkBytes")
            else:
                changed["brokers"][0]["watermarkBytes"] = bad
            self.assertFalse(probe.budget_matches(changed))
        changed = copy.deepcopy(snapshot)
        changed["operatorMemoryOverride"] = 858993460
        self.assertFalse(probe.budget_matches(changed))

    def test_missing_override_is_unknown(self):
        with self.assertRaises(RuntimeError):
            probe.memory_override("disk_free_limit.absolute = 128MiB\n")

    def test_operator_formatted_override(self):
        self.assertEqual(probe.memory_override(
            "total_memory_available_override_value = 858993460\ndisk_free_limit.absolute = 128MiB\n"), 858993460)


if __name__ == "__main__":
    unittest.main()
