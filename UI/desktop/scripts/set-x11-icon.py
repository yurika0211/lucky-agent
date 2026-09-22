#!/usr/bin/env python3
"""Write _NET_WM_ICON through XChangeProperty.

Input blob: uint32 width, uint32 height, then BGRA pixels top-to-bottom.
xprop -set cannot do this; it truncates CARDINAL properties at 64 values.
"""
import ctypes
import ctypes.util
import struct
import sys


def load_lib(name):
    path = ctypes.util.find_library(name)
    if not path:
        raise SystemExit(f'missing library: {name}')
    return ctypes.CDLL(path)


def main():
    if len(sys.argv) != 3:
        raise SystemExit('usage: set-x11-icon.py <window-id> <rgba-blob>')
    wid = int(sys.argv[1], 0)
    blob = open(sys.argv[2], 'rb').read()
    width, height = struct.unpack_from('<II', blob, 0)
    pixels = blob[8:]
    expected = width * height * 4
    if width <= 0 or height <= 0 or len(pixels) < expected:
        raise SystemExit(f'bad icon blob: {width}x{height} bytes={len(pixels)}')

    x11 = load_lib('X11')
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XInternAtom.restype = ctypes.c_ulong
    x11.XChangeProperty.argtypes = [
        ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_ulong,
        ctypes.c_int, ctypes.c_int, ctypes.c_void_p, ctypes.c_int,
    ]
    x11.XFlush.argtypes = [ctypes.c_void_p]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]

    display = x11.XOpenDisplay(None)
    if not display:
        raise SystemExit('XOpenDisplay failed')
    try:
        atom = x11.XInternAtom(display, b'_NET_WM_ICON', False)
        xa_cardinal = 6  # XA_CARDINAL
        prop_replace = 0
        values = (ctypes.c_ulong * (2 + width * height))()
        values[0] = width
        values[1] = height
        for i in range(width * height):
            b, g, r, a = pixels[i * 4:i * 4 + 4]
            values[2 + i] = ((a << 24) | (r << 16) | (g << 8) | b) & 0xFFFFFFFF
        status = x11.XChangeProperty(
            display, wid, atom, xa_cardinal, 32, prop_replace,
            ctypes.cast(values, ctypes.c_void_p), len(values),
        )
        x11.XFlush(display)
        if status == 0:
            raise SystemExit('XChangeProperty failed')
    finally:
        x11.XCloseDisplay(display)


if __name__ == '__main__':
    main()
