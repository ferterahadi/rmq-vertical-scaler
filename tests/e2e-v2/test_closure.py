import unittest
from types import SimpleNamespace
from closure import Closure, verified_image, configuration_value


def pod(uid, digest="sha256:good"):
    return {"metadata": {"uid": uid}, "spec": {"containers": [{"image": "test@sha256:good"}]},
            "status": {"containerStatuses": [{"ready": True, "imageID": "test@" + digest}]}}


class ImageSafetyTests(unittest.TestCase):
    def test_cleanup_closes_namespace_before_waiting_for_cr_absence(self):
        # A stale Operator deletion reconcile can recreate a default CR while
        # the namespace remains Active. Namespace admission must close first.
        events = []
        state = {"namespaceDeleting": False}
        run = Closure.__new__(Closure)
        run.namespace = "owned-test"
        run.args = SimpleNamespace()
        run.init_names = ["setup-container"]
        run.close = lambda: None
        def exists(kind, name, cluster=False):
            if kind == "namespace":
                return None if state["namespaceDeleting"] else {"metadata": {"labels": {
                    "rmq-vertical-scaler-test": "v23-feasibility"}}}
            if kind == "statefulset":
                return None
            return {"metadata": {"uid": "operator-recreated-cr"}}
        run.exists = exists
        def kubectl(*args):
            events.append(args[:2])
            if args[:2] == ("delete", "namespace"):
                state["namespaceDeleting"] = True
        def wait(description, predicate, timeout):
            if not predicate():
                raise RuntimeError("Operator can recreate CR until namespace admission closes")
        run.probe = SimpleNamespace(kubectl=kubectl, wait=wait)
        class ReadyForFixture(Exception):
            pass
        def apply(*args):
            self.assertTrue(state["namespaceDeleting"])
            raise ReadyForFixture()
        run.apply = apply
        with self.assertRaises(ReadyForFixture):
            run.fresh()
        self.assertLess(events.index(("delete", "rabbitmqcluster")), events.index(("delete", "namespace")))

    def test_generated_configuration_accepts_aligned_spacing_but_not_wrong_or_duplicate_values(self):
        self.assertEqual(configuration_value({"user.conf": "channel_max                           = 1024\n"}, "channel_max"), "1024")
        self.assertNotEqual(configuration_value({"user.conf": "channel_max = 512\n"}, "channel_max"), "1024")
        self.assertIsNone(configuration_value({"user.conf": "channel_max = 1024\nchannel_max = 512\n"}, "channel_max"))

    def test_digest_identity_accepts_runtime_repository_prefix(self):
        self.assertEqual(verified_image([pod("one")], "sha256:good")["podUID"], "one")

    def test_wrong_image_and_overlapping_replicas_fail_without_retry(self):
        for pods in ([pod("one", "sha256:wrong")], [pod("one"), pod("two")]):
            with self.assertRaises(AssertionError):
                verified_image(pods, "sha256:good")

    def test_no_ready_pod_is_retryable_but_terminating_pod_is_ignored(self):
        self.assertIsNone(verified_image([], "sha256:good"))
        old = pod("old")
        old["metadata"]["deletionTimestamp"] = "2026-10-29T00:00:00Z"
        self.assertEqual(verified_image([old, pod("new")], "sha256:good")["podUID"], "new")


if __name__ == "__main__":
    unittest.main()
