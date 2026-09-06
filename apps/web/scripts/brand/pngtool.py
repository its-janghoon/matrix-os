"""Minimal PNG decode / area-average downscale / encode, stdlib only.

Chromium refuses to screenshot a small window: it clamps both the viewport and
--force-device-scale-factor (to 0.5), so asking it for a 16x16 PNG yields a
blank or clipped image. Rendering once at 512 and resampling here sidesteps
that entirely, and there is no image library in this environment to do it with.
"""
import struct, zlib


def decode(path):
    """Decode an 8-bit RGB/RGBA, non-interlaced PNG to (w, h, bytearray RGBA)."""
    data = open(path, "rb").read()
    assert data[:8] == b"\x89PNG\r\n\x1a\x0a", f"{path}: not a PNG"
    pos, idat, w = 8, bytearray(), None
    while pos < len(data):
        ln, typ = struct.unpack(">I4s", data[pos:pos + 8])
        body = data[pos + 8:pos + 8 + ln]
        if typ == b"IHDR":
            w, h, depth, color, comp, filt, interlace = struct.unpack(">IIBBBBB", body)
            assert depth == 8 and color in (2, 6) and interlace == 0, \
                f"{path}: unsupported PNG (depth={depth} color={color} interlace={interlace})"
            channels = 4 if color == 6 else 3
        elif typ == b"IDAT":
            idat += body
        elif typ == b"IEND":
            break
        pos += 12 + ln
    assert w is not None, f"{path}: no IHDR"

    raw = zlib.decompress(bytes(idat))
    stride = w * channels
    out = bytearray(w * h * 4)
    prev = bytearray(stride)
    p = 0
    for y in range(h):
        ft = raw[p]; p += 1
        line = bytearray(raw[p:p + stride]); p += stride
        # Undo the per-scanline filter (PNG spec 9.2).
        if ft == 1:
            for i in range(channels, stride):
                line[i] = (line[i] + line[i - channels]) & 0xFF
        elif ft == 2:
            for i in range(stride):
                line[i] = (line[i] + prev[i]) & 0xFF
        elif ft == 3:
            for i in range(stride):
                a = line[i - channels] if i >= channels else 0
                line[i] = (line[i] + ((a + prev[i]) >> 1)) & 0xFF
        elif ft == 4:
            for i in range(stride):
                a = line[i - channels] if i >= channels else 0
                c = prev[i - channels] if i >= channels else 0
                b = prev[i]
                pa, pb, pc = abs(b - c), abs(a - c), abs(a + b - 2 * c)
                pr = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pr) & 0xFF
        elif ft != 0:
            raise AssertionError(f"{path}: bad filter type {ft}")
        for x in range(w):
            s, d = x * channels, (y * w + x) * 4
            out[d] = line[s]; out[d + 1] = line[s + 1]; out[d + 2] = line[s + 2]
            out[d + 3] = line[s + 3] if channels == 4 else 255
        prev = line
    return w, h, out


def resize(w, h, px, tw, th):
    """Area-average resample. Colour is premultiplied by alpha before averaging
    so a transparent edge does not drag dark fringes into the result."""
    out = bytearray(tw * th * 4)
    for ty in range(th):
        y0, y1 = ty * h // th, max(ty * h // th + 1, (ty + 1) * h // th)
        for tx in range(tw):
            x0, x1 = tx * w // tw, max(tx * w // tw + 1, (tx + 1) * w // tw)
            r = g = b = a = n = 0
            for y in range(y0, y1):
                base = y * w
                for x in range(x0, x1):
                    i = (base + x) * 4
                    al = px[i + 3]
                    r += px[i] * al; g += px[i + 1] * al; b += px[i + 2] * al
                    a += al; n += 1
            d = (ty * tw + tx) * 4
            if a:
                out[d] = min(255, r // a); out[d + 1] = min(255, g // a); out[d + 2] = min(255, b // a)
            out[d + 3] = a // n
    return out


def encode(w, h, px, path):
    """Encode 8-bit RGBA, filter 0, max deflate."""
    raw = bytearray()
    for y in range(h):
        raw.append(0)
        raw += px[y * w * 4:(y + 1) * w * 4]

    def chunk(typ, body):
        return (struct.pack(">I", len(body)) + typ + body
                + struct.pack(">I", zlib.crc32(typ + body) & 0xFFFFFFFF))

    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\x0a")
        f.write(chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0)))
        f.write(chunk(b"IDAT", zlib.compress(bytes(raw), 9)))
        f.write(chunk(b"IEND", b""))


def crop(w, h, px, x0, y0, cw, ch):
    """Cut a cw x ch rect out at (x0, y0).

    Needed because --window-size is the OUTER window: headless Chromium
    subtracts its chrome from the layout viewport (87px of height, measured),
    so asking for a 1200x630 window lays out at 1200x543 and pads the
    screenshot with white. Rendering into a generous window and cropping the
    top-left avoids depending on that constant at all.
    """
    assert x0 + cw <= w and y0 + ch <= h, \
        f"crop {cw}x{ch} at ({x0},{y0}) does not fit in {w}x{h}"
    out = bytearray(cw * ch * 4)
    for y in range(ch):
        src = ((y0 + y) * w + x0) * 4
        out[y * cw * 4:(y + 1) * cw * 4] = px[src:src + cw * 4]
    return out
