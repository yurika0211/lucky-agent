import importlib.util
from pathlib import Path
import struct
import tempfile
import threading
import unittest
from unittest.mock import Mock

spec = importlib.util.spec_from_file_location("desktop_helper", Path(__file__).with_name("desktop_helper.py"))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)


class PortalTests(unittest.TestCase):
    def portal(self):
        portal = helper.Portal.__new__(helper.Portal)
        portal.check = lambda: None
        portal.notify = Mock()
        portal.observe_only = False
        portal.stream = 42
        portal.devices = 3
        portal.properties = {"size": (1920, 1080)}
        portal.observed_size = (3840, 2160)
        portal.latest = b"\x89PNG\r\n\x1a\n" + bytes(8) + struct.pack(">II", 3840, 2160)
        portal.current_size = (3840, 2160)
        return portal

    def test_hidpi_pointer_and_evdev_button(self):
        portal = self.portal()
        portal.action(dict(kind="click", display_id="portal:42", x=1920, y=1080, button="right"))
        self.assertEqual(portal.notify.call_args_list[0].args,
                         ("NotifyPointerMotionAbsolute", "udd", 42, 960.0, 540.0))
        self.assertEqual(portal.notify.call_args_list[1].args, ("NotifyPointerButton", "iu", 273, 1))
        self.assertEqual(portal.notify.call_args_list[2].args, ("NotifyPointerButton", "iu", 273, 0))

    def test_logical_size_overrides_legacy_size(self):
        portal = self.portal()
        portal.properties["logical_size"] = (2560, 1440)
        portal.point(1920, 1080)
        self.assertEqual(portal.notify.call_args.args, ("NotifyPointerMotionAbsolute", "udd", 42, 1280.0, 720.0))

    def test_reject_stale_stream_and_resolution_without_input(self):
        for display, size in [("portal:999", (3840, 2160)), ("portal:42", (1600, 900))]:
            portal = self.portal()
            portal.observed_size = size
            with self.assertRaises(RuntimeError):
                portal.action(dict(kind="click", display_id=display))
            portal.notify.assert_not_called()

    def test_no_keyboard_permission(self):
        portal = self.portal()
        portal.devices = 2
        with self.assertRaises(RuntimeError):
            portal.action(dict(kind="keypress", display_id="portal:42", keys=["CTRL", "a"]))
        portal.notify.assert_not_called()

    def test_key_validation_precedes_press(self):
        portal = self.portal()
        with self.assertRaises(RuntimeError):
            portal.chord(["CTRL", "unknown_key"])
        portal.notify.assert_not_called()

    def test_keys_released_in_reverse_order(self):
        portal = self.portal()
        portal.chord(["CTRL", "a"])
        calls = [c.args for c in portal.notify.call_args_list]
        self.assertEqual(calls, [("NotifyKeyboardKeysym", "iu", 0xffe3, 1),
                                 ("NotifyKeyboardKeysym", "iu", ord("a"), 1),
                                 ("NotifyKeyboardKeysym", "iu", ord("a"), 0),
                                 ("NotifyKeyboardKeysym", "iu", 0xffe3, 0)])

    def test_drag_releases_button_on_failure(self):
        portal = self.portal()
        portal.point = Mock(side_effect=[None, RuntimeError("stream lost")])
        with self.assertRaises(RuntimeError):
            portal.action(dict(kind="drag", display_id="portal:42", end_x=10, end_y=20))
        self.assertEqual(portal.notify.call_args.args, ("NotifyPointerButton", "iu", 272, 0))

    def test_unicode_keysym(self):
        self.assertEqual(helper.Portal.keysym("中"), 0x01000000 | ord("中"))

    def test_permission_response_and_subscription_cleanup(self):
        for status in (0, 1, 2):
            portal = helper.Portal.__new__(helper.Portal)
            portal.Gio = Mock()
            portal.GLib = Mock()
            portal.GLib.Variant.side_effect = lambda signature, value: value
            portal.GLib.timeout_add_seconds.return_value = 7
            portal.bus = Mock()
            portal.bus.get_unique_name.return_value = ":1.88"
            callbacks = []
            portal.bus.signal_subscribe.side_effect = lambda *args: callbacks.append(args[-1]) or 9

            def call(interface, method, signature, values):
                handle = "/org/freedesktop/portal/desktop/request/1_88/" + values[-1]["handle_token"]
                params = Mock()
                params.unpack.return_value = (status, {"session_handle": "/test/session"})
                callbacks[0](None, None, handle, None, None, params)
                return (handle,)

            portal.call = call
            if status == 0:
                self.assertEqual(portal.request(portal.REMOTE, "CreateSession", "(a{sv})", ({},)),
                                 {"session_handle": "/test/session"})
            else:
                with self.assertRaisesRegex(RuntimeError, "cancelled or denied"):
                    portal.request(portal.REMOTE, "CreateSession", "(a{sv})", ({},))
            portal.bus.signal_unsubscribe.assert_called_once_with(9)
            portal.GLib.source_remove.assert_called_once_with(7)

    def test_revoked_session_rejected(self):
        portal = self.portal()
        portal.GLib = Mock()
        portal.GLib.MainContext.default().pending.return_value = False
        portal.revoked = True
        with self.assertRaisesRegex(RuntimeError, "revoked"):
            helper.Portal.check(portal)


class AccessibilityTests(unittest.TestCase):
    def setUp(self):
        self.original = helper.atspi
        api = Mock()
        api.StateType.DEFUNCT = "defunct"
        api.StateType.SHOWING = "showing"
        api.StateType.SENSITIVE = "sensitive"
        helper.atspi = lambda: api
        self.node = Mock()
        self.node.get_state_set().contains.side_effect = lambda state: state in ("showing", "sensitive")
        self.node.get_name.return_value = "Input"
        self.node.get_role_name.return_value = "text"
        helper._nodes["test"] = (self.node, "Input", "text")

    def tearDown(self):
        helper.atspi = self.original
        helper._nodes.clear()

    def test_set_text_preserves_unicode_and_whitespace(self):
        helper.a11y_action(dict(kind="set_text", element_id="test", text=" 你好\n"))
        self.node.get_editable_text_iface().set_text_contents.assert_called_once_with(" 你好\n")

    def test_changed_node_rejected(self):
        self.node.get_name.return_value = "Different control"
        with self.assertRaises(RuntimeError):
            helper.a11y_action(dict(kind="set_text", element_id="test", text="hello"))
        self.node.get_editable_text_iface.assert_not_called()

    def test_named_action_and_application_rejection(self):
        action = self.node.get_action_iface()
        action.get_n_actions.return_value = 2
        action.get_action_name.side_effect = ["press", "menu"]
        action.do_action.return_value = False
        with self.assertRaises(RuntimeError):
            helper.a11y_action(dict(kind="invoke", element_id="test", element_action="menu"))
        action.do_action.assert_called_once_with(1)


class GStreamerTests(unittest.TestCase):
    def test_raw_frame_encoded_on_request(self):
        try:
            import gi
            gi.require_version("Gst", "1.0")
            gi.require_version("GstApp", "1.0")
            from gi.repository import Gst
        except (ImportError, ValueError):
            self.skipTest("GStreamer GI is an optional desktop dependency")
        Gst.init(None)
        if not all(Gst.ElementFactory.find(name) for name in ("videotestsrc", "appsrc", "appsink", "pngenc")):
            self.skipTest("GStreamer test/PNG plugins are unavailable")
        portal = helper.Portal.__new__(helper.Portal)
        portal.Gst = Gst
        portal.condition = threading.Condition()
        portal.latest = portal.encoded_sample = portal.encoded_data = None
        portal.current_size = portal.observed_size = None
        portal.stream = 42
        portal.check = lambda: None
        pipeline = Gst.parse_launch("videotestsrc num-buffers=1 ! video/x-raw,format=RGB,width=64,height=32 ! appsink name=sink sync=false emit-signals=true")
        pipeline.get_by_name("sink").connect("new-sample", portal.on_sample)
        portal.encoder = Gst.parse_launch("appsrc name=source is-live=true format=time ! pngenc ! appsink name=sink sync=false max-buffers=1 drop=true")
        try:
            portal.encoder.set_state(Gst.State.PLAYING)
            pipeline.set_state(Gst.State.PLAYING)
            with tempfile.TemporaryDirectory() as directory:
                path = str(Path(directory) / "frame.png")
                result = portal.capture(path, "")
                self.assertEqual((result["width"], result["height"]), (64, 32))
                first = Path(path).read_bytes()
                self.assertEqual(first[:8], b"\x89PNG\r\n\x1a\n")
                portal.capture(path, "portal:42")
                self.assertEqual(Path(path).read_bytes(), first)
        finally:
            pipeline.set_state(Gst.State.NULL)
            portal.encoder.set_state(Gst.State.NULL)


if __name__ == "__main__":
    unittest.main()
