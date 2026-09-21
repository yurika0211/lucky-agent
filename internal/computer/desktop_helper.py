"""Private JSON-lines helper embedded in LuckyAgent; no shell or network API.

GI is loaded lazily so X11 screenshots do not depend on Python or AT-SPI.
The process owns all D-Bus sessions; terminating it revokes its portal session.
"""

import collections
import json
import os
import struct
import sys
import threading
import time
import uuid

_snapshots = collections.deque()
_nodes = {}
_portal = None


def atspi():
    import gi
    gi.require_version("Atspi", "2.0")
    from gi.repository import Atspi
    Atspi.init()
    Atspi.set_timeout(750, 1500)
    return Atspi


def a11y_observe(window):
    api = atspi()
    desktop = api.get_desktop(0)
    if desktop is None:
        raise RuntimeError("AT-SPI desktop is unavailable; enable accessibility in the desktop session")
    roots = []
    for ai in range(min(desktop.get_child_count(), 100)):
        app = desktop.get_child_at_index(ai)
        if app is None:
            continue
        for wi in range(min(app.get_child_count(), 100)):
            try:
                node = app.get_child_at_index(wi)
                name = node.get_name() or ""
                if not window or name.casefold() == window.casefold():
                    roots.append(node)
            except Exception:
                continue
    if window and not roots:
        raise RuntimeError("AT-SPI cannot find the selected window; use image observations for this application")
    if window and len(roots) > 1:
        raise RuntimeError("AT-SPI window title is ambiguous; give the application a unique title")
    prefix = uuid.uuid4().hex
    queue = collections.deque((node, "", 0) for node in roots)
    records, ids = [], []
    visited = 0
    while queue and len(records) < 200 and visited < 1000:
        node, parent, depth = queue.popleft()
        visited += 1
        if node is None:
            continue
        try:
            state = node.get_state_set()
            if state.contains(api.StateType.DEFUNCT):
                continue
            # Hidden subtrees cannot supply actionable on-screen controls.
            if not state.contains(api.StateType.SHOWING):
                continue
            name, role = (node.get_name() or "")[:256], node.get_role_name()
            interfaces = node.get_interfaces() or []
            supports = lambda suffix: any(str(i).split(".")[-1] == suffix for i in interfaces)
            bounds = dict(x=0, y=0, width=0, height=0)
            if supports("Component"):
                rect = node.get_component_iface().get_extents(api.CoordType.SCREEN)
                bounds = dict(x=rect.x, y=rect.y, width=rect.width, height=rect.height)
            actions = []
            if supports("Action"):
                action = node.get_action_iface()
                actions = [action.get_action_name(i) for i in range(min(action.get_n_actions(), 20))]
            ident = prefix + ":" + str(len(records))
            records.append(dict(id=ident, parent_id=parent, name=name, role=role, bounds=bounds,
                                actions=actions, editable=supports("EditableText"),
                                focusable=state.contains(api.StateType.FOCUSABLE),
                                enabled=state.contains(api.StateType.SENSITIVE)))
            _nodes[ident] = (node, name, role)
            ids.append(ident)
            if depth < 12:
                for ci in range(min(node.get_child_count(), 200)):
                    if len(queue) >= 1000:
                        break
                    queue.append((node.get_child_at_index(ci), ident, depth + 1))
        except Exception:
            continue
    _snapshots.append(ids)
    while len(_snapshots) > 8:
        for ident in _snapshots.popleft():
            _nodes.pop(ident, None)
    return dict(window=window, nodes=records, truncated=bool(queue))


def a11y_action(action):
    api = atspi()
    entry = _nodes.get(action.get("element_id"))
    if entry is None:
        raise RuntimeError("stale accessibility element; observe its tree again")
    node, name, role = entry
    state = node.get_state_set()
    if (state.contains(api.StateType.DEFUNCT) or not state.contains(api.StateType.SHOWING)
            or not state.contains(api.StateType.SENSITIVE)
            or (node.get_name() or "")[:256] != name or node.get_role_name() != role):
        raise RuntimeError("accessibility element changed or is unavailable; observe again")
    kind = action["kind"]
    if kind == "focus":
        success = node.get_component_iface().grab_focus()
    elif kind == "set_text":
        success = node.get_editable_text_iface().set_text_contents(action.get("text", ""))
    elif kind == "invoke":
        iface = node.get_action_iface()
        names = [iface.get_action_name(i) for i in range(iface.get_n_actions())]
        requested = action.get("element_action", "")
        if not requested:
            requested = next((name for name in ("click", "press", "activate") if name in names), "")
        if not requested and len(names) == 1:
            requested = names[0]
        if requested not in names:
            raise RuntimeError("choose an element_action from the observed control's actions")
        success = iface.do_action(names.index(requested))
    else:
        raise RuntimeError("unsupported accessibility action")
    if not success:
        raise RuntimeError("application rejected the accessibility action")
    return {}


class Portal:
    BUS = "org.freedesktop.portal.Desktop"
    PATH = "/org/freedesktop/portal/desktop"
    SCREEN = "org.freedesktop.portal.ScreenCast"
    REMOTE = "org.freedesktop.portal.RemoteDesktop"

    def __init__(self, observe_only, start=True):
        import gi
        gi.require_version("Gst", "1.0")
        gi.require_version("GstApp", "1.0")
        from gi.repository import Gio, GLib, Gst
        self.Gio, self.GLib, self.Gst = Gio, GLib, Gst
        Gst.init(None)
        for element in ("pipewiresrc", "videoconvert", "pngenc", "appsrc", "appsink"):
            if not Gst.ElementFactory.find(element):
                raise RuntimeError("Wayland capture requires GStreamer plugin " + element)
        self.bus = Gio.bus_get_sync(Gio.BusType.SESSION, None)
        self.observe_only = observe_only
        self.session, self.pipeline, self.encoder, self.fd = None, None, None, None
        self.latest, self.stream, self.properties, self.devices = None, None, {}, 0
        self.condition = threading.Condition()
        self.revoked, self.closed_subscription = False, None
        self.observed_size = None
        self.current_size = None
        self.encoded_sample, self.encoded_data = None, None
        try:
            self.call("org.freedesktop.DBus.Properties", "Get", "(ss)", (self.SCREEN, "AvailableSourceTypes"))
            if not observe_only:
                self.call("org.freedesktop.DBus.Properties", "Get", "(ss)", (self.REMOTE, "AvailableDeviceTypes"))
            if start:
                self.start()
        except Exception:
            self.close()
            raise

    def call(self, interface, method, signature, values, path=None):
        return self.bus.call_sync(self.BUS, path or self.PATH, interface, method,
                                  self.GLib.Variant(signature, values), None,
                                  self.Gio.DBusCallFlags.NONE, 10000, None).unpack()

    def request(self, interface, method, signature, values):
        token = "la_" + uuid.uuid4().hex
        options = dict(values[-1])
        options["handle_token"] = self.GLib.Variant("s", token)
        values = (*values[:-1], options)
        handle = "/org/freedesktop/portal/desktop/request/" + self.bus.get_unique_name()[1:].replace(".", "_") + "/" + token
        response = []
        loop = self.GLib.MainLoop()

        def replied(conn, sender, path, iface, signal, params):
            if path == handle:
                response.append(params.unpack())
                loop.quit()

        sub = self.bus.signal_subscribe(self.BUS, "org.freedesktop.portal.Request", "Response",
                                        None, None, self.Gio.DBusSignalFlags.NONE, replied)
        timer = self.GLib.timeout_add_seconds(55, lambda: (loop.quit(), False)[1])
        try:
            handle = self.call(interface, method, signature, values)[0]
            if not response:
                loop.run()
            if not response:
                self.call("org.freedesktop.portal.Request", "Close", "()", (), path=handle)
                raise RuntimeError("desktop portal consent timed out")
            status, results = response[0]
            if status != 0:
                raise RuntimeError("desktop portal permission was cancelled or denied (response %s)" % status)
            return results
        finally:
            self.bus.signal_unsubscribe(sub)
            self.GLib.source_remove(timer)

    def start(self):
        variant = self.GLib.Variant
        interface = self.SCREEN if self.observe_only else self.REMOTE
        result = self.request(interface, "CreateSession", "(a{sv})",
                              ({"session_handle_token": variant("s", "la_" + uuid.uuid4().hex)},))
        self.session = result["session_handle"]
        self.closed_subscription = self.bus.signal_subscribe(
            self.BUS, "org.freedesktop.portal.Session", "Closed", self.session,
            None, self.Gio.DBusSignalFlags.NONE, self.session_closed)
        if not self.observe_only:
            self.request(self.REMOTE, "SelectDevices", "(oa{sv})", (self.session, {"types": variant("u", 3)}))
        self.request(self.SCREEN, "SelectSources", "(oa{sv})", (self.session, {
            "types": variant("u", 1), "multiple": variant("b", False)}))
        result = self.request(interface, "Start", "(osa{sv})", (self.session, "", {}))
        streams = result.get("streams", [])
        if len(streams) != 1:
            raise RuntimeError("select exactly one monitor in the desktop portal")
        self.stream, self.properties = streams[0]
        self.devices = result.get("devices", 0)
        reply, fds = self.bus.call_with_unix_fd_list_sync(
            self.BUS, self.PATH, self.SCREEN, "OpenPipeWireRemote",
            variant("(oa{sv})", (self.session, {})), None,
            self.Gio.DBusCallFlags.NONE, 10000, None, None)
        self.fd = fds.get(reply.unpack()[0])
        self.pipeline = self.Gst.parse_launch(
            "pipewiresrc name=source do-timestamp=true ! videoconvert ! video/x-raw,format=RGB ! "
            "appsink name=sink sync=false emit-signals=true max-buffers=1 drop=true")
        source = self.pipeline.get_by_name("source")
        source.set_property("fd", self.fd)
        serial = self.properties.get("pipewire-serial")
        if serial is not None:
            properties = self.Gst.Structure.new_empty("props")
            properties.set_value("target.object", str(serial))
            source.set_property("stream-properties", properties)
        else:
            source.set_property("path", str(self.stream))
        sink = self.pipeline.get_by_name("sink")
        sink.connect("new-sample", self.on_sample)
        if self.pipeline.set_state(self.Gst.State.PLAYING) == self.Gst.StateChangeReturn.FAILURE:
            raise RuntimeError("PipeWire capture pipeline failed to start")
        # Encode only when the agent asks for a screenshot, rather than running
        # PNG compression at the monitor's frame rate throughout the session.
        self.encoder = self.Gst.parse_launch(
            "appsrc name=source is-live=true format=time ! pngenc ! "
            "appsink name=sink sync=false max-buffers=1 drop=true")
        if self.encoder.set_state(self.Gst.State.PLAYING) == self.Gst.StateChangeReturn.FAILURE:
            raise RuntimeError("PNG encoder failed to start")

    def session_closed(self, *args):
        self.revoked = True

    def on_sample(self, sink):
        sample = sink.emit("pull-sample")
        if sample is not None:
            caps = sample.get_caps().get_structure(0)
            with self.condition:
                self.latest = sample
                self.current_size = (caps.get_value("width"), caps.get_value("height"))
                self.condition.notify_all()
        return self.Gst.FlowReturn.OK

    def check(self):
        context = self.GLib.MainContext.default()
        while context.pending():
            context.iteration(False)
        if self.revoked:
            raise RuntimeError("desktop portal session was revoked; observe again to request access")
        if self.pipeline is not None:
            error = self.pipeline.get_bus().pop_filtered(self.Gst.MessageType.ERROR | self.Gst.MessageType.EOS)
            if error is not None:
                self.revoked = True
                raise RuntimeError("PipeWire stream ended; observe again to request access")

    def capture(self, path, display_id):
        self.check()
        current_id = "portal:" + str(self.stream)
        if display_id and display_id != current_id:
            raise RuntimeError("display_id does not match the portal-selected monitor")
        with self.condition:
            if self.latest is None:
                self.condition.wait_for(lambda: self.latest is not None, timeout=5)
            sample = self.latest
        self.check()
        if sample is None:
            raise RuntimeError("PipeWire did not provide a frame; check the portal and GStreamer plugins")
        if sample is self.encoded_sample:
            data = self.encoded_data
        else:
            source = self.encoder.get_by_name("source")
            source.set_property("caps", sample.get_caps())
            if source.emit("push-buffer", sample.get_buffer().copy()) != self.Gst.FlowReturn.OK:
                raise RuntimeError("PNG encoder rejected the PipeWire frame")
            encoded = self.encoder.get_by_name("sink").emit("try-pull-sample", 5 * self.Gst.SECOND)
            if encoded is None:
                raise RuntimeError("PNG encoder timed out")
            buf = encoded.get_buffer()
            data = buf.extract_dup(0, buf.get_size())
            self.encoded_sample, self.encoded_data = sample, data
        if len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n":
            raise RuntimeError("PipeWire produced an invalid PNG")
        width, height = struct.unpack(">II", data[16:24])
        with open(path, "wb") as output:
            output.write(data)
        self.observed_size = (width, height)
        return dict(width=width, height=height, display_id=current_id, scale_factor=1)

    def notify(self, method, signature, *args):
        return self.call(self.REMOTE, method, "(oa{sv}" + signature + ")", (self.session, {}, *args))

    def point(self, x, y):
        width, height = self.observed_size
        # Portal input is logical, while PNG pixels may be scaled by HiDPI.
        logical = self.properties.get("logical_size", self.properties.get("size", (width, height)))
        self.notify("NotifyPointerMotionAbsolute", "udd", self.stream,
                    float(x) * logical[0] / width, float(y) * logical[1] / height)

    @staticmethod
    def keysym(key):
        aliases = {"ctrl": 0xffe3, "control": 0xffe3, "alt": 0xffe9, "shift": 0xffe1,
                   "super": 0xffeb, "win": 0xffeb, "meta": 0xffeb, "cmd": 0xffeb,
                   "enter": 0xff0d, "return": 0xff0d, "escape": 0xff1b, "esc": 0xff1b,
                   "tab": 0xff09, "backspace": 0xff08, "delete": 0xffff, "space": 0x20,
                   "left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54,
                   "home": 0xff50, "end": 0xff57, "pageup": 0xff55, "pagedown": 0xff56}
        if key.lower() in aliases:
            return aliases[key.lower()]
        if key.lower().startswith("f") and key[1:].isdigit() and 1 <= int(key[1:]) <= 24:
            return 0xffbd + int(key[1:])
        if len(key) == 1:
            code = ord(key)
            return code if code <= 0xff else 0x01000000 | code
        raise RuntimeError("unsupported key: " + key)

    def chord(self, keys):
        syms = [self.keysym(key) for key in keys]  # Validate before pressing anything.
        pressed = []
        try:
            for sym in syms:
                self.notify("NotifyKeyboardKeysym", "iu", sym, 1)
                pressed.append(sym)
        finally:
            for sym in reversed(pressed):
                self.notify("NotifyKeyboardKeysym", "iu", sym, 0)

    def action(self, action):
        self.check()
        if self.observe_only or self.observed_size is None:
            raise RuntimeError("observe a control-enabled Wayland session before acting")
        if action.get("display_id") != "portal:" + str(self.stream):
            raise RuntimeError("stale portal stream; observe again")
        if self.current_size is not None and self.current_size != self.observed_size:
            raise RuntimeError("monitor resolution changed; observe again")
        kind = action["kind"]
        if kind in ("type", "keypress"):
            if not self.devices & 1:
                raise RuntimeError("keyboard access was not granted by the portal")
            if kind == "keypress":
                self.chord(action.get("keys", []))
            else:
                for char in action.get("text", ""):
                    self.chord([{"\n": "Return", "\t": "Tab"}.get(char, char)])
            return {}
        if not self.devices & 2:
            raise RuntimeError("pointer access was not granted by the portal")
        button = {"left": 272, "right": 273, "middle": 274}.get(action.get("button", "left"), 272)
        if kind in ("click", "double_click", "move", "drag"):
            self.point(action.get("x", 0), action.get("y", 0))
        if kind in ("click", "double_click", "drag"):
            for i in range(2 if kind == "double_click" else 1):
                self.notify("NotifyPointerButton", "iu", button, 1)
                try:
                    if kind == "drag":
                        duration = max(0, min(10000, action.get("duration_ms", 0))) / 1000
                        steps = max(1, min(60, int(duration * 60)))
                        for step in range(1, steps + 1):
                            if duration:
                                time.sleep(duration / steps)
                            ratio = step / steps
                            self.point(action.get("x", 0) + (action.get("end_x", 0) - action.get("x", 0)) * ratio,
                                       action.get("y", 0) + (action.get("end_y", 0) - action.get("y", 0)) * ratio)
                finally:
                    self.notify("NotifyPointerButton", "iu", button, 0)
                if kind == "double_click" and i == 0:
                    time.sleep(0.08)
        elif kind == "scroll":
            for axis, key in ((0, "delta_y"), (1, "delta_x")):
                if action.get(key):
                    self.notify("NotifyPointerAxisDiscrete", "ui", axis, action[key])
        elif kind != "move":
            raise RuntimeError("unsupported Wayland input action")
        return {}

    def close(self):
        if self.encoder is not None:
            self.encoder.set_state(self.Gst.State.NULL)
            self.encoder = None
        if self.pipeline is not None:
            self.pipeline.set_state(self.Gst.State.NULL)
            self.pipeline = None
        if self.closed_subscription is not None:
            self.bus.signal_unsubscribe(self.closed_subscription)
            self.closed_subscription = None
        if self.session:
            try:
                self.call("org.freedesktop.portal.Session", "Close", "()", (), path=self.session)
            except Exception:
                pass
            self.session = None
        if self.fd is not None:
            os.close(self.fd)
            self.fd = None
        self.latest, self.encoded_sample, self.encoded_data = None, None, None


def wayland_request(request):
    global _portal
    op = request["op"]
    if op == "wayland_probe":
        probe = Portal(request.get("observe_only", False), start=False)
        probe.close()
        return {}
    if op == "wayland_capture":
        if _portal is not None:
            try:
                _portal.check()
            except Exception:
                _portal.close()
                _portal = None
        if _portal is None:
            _portal = Portal(request.get("observe_only", False))
        return _portal.capture(request["path"], request.get("display_id", ""))
    if op == "wayland_action" and _portal is not None:
        return _portal.action(request["action"])
    if op == "wayland_validate" and _portal is not None:
        _portal.check()
        if (request.get("display_id") != "portal:" + str(_portal.stream)
                or (request.get("width"), request.get("height")) != _portal.current_size):
            raise RuntimeError("portal stream or monitor resolution changed since this frame; observe again")
        return {}
    raise RuntimeError("observe the Wayland desktop before acting")


def dispatch(request):
    op = request.get("op")
    if op == "a11y_observe":
        return a11y_observe(request.get("window", ""))
    if op == "a11y_action":
        return a11y_action(request["action"])
    if op.startswith("wayland_"):
        return wayland_request(request)
    raise RuntimeError("unsupported desktop operation")


def main():
    for line in sys.stdin:
        try:
            request = json.loads(line)
            result = dispatch(request)
            response = {"result": result}
        except Exception as exc:
            response = {"error": str(exc)}
        print(json.dumps(response, ensure_ascii=True), flush=True)


if __name__ == "__main__":
    main()
