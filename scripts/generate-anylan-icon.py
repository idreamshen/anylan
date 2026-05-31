#!/usr/bin/env python3
import math
import struct
import zlib
from pathlib import Path


def png_rgba(size):
    width = height = size
    rows = []
    for y in range(height):
        row = bytearray([0])
        for x in range(width):
            nx = (x + 0.5) / width
            ny = (y + 0.5) / height

            px = abs(nx - 0.5) - 0.42
            py = abs(ny - 0.5) - 0.42
            qx = max(px, 0)
            qy = max(py, 0)
            dist = math.sqrt(qx * qx + qy * qy) + min(max(px, py), 0)
            alpha = 255 if dist < 0 else 0

            red = int(21 + 25 * nx)
            green = int(89 + 35 * ny)
            blue = int(184 + 45 * (1 - ny))

            def line_alpha(x1, y1, x2, y2, width):
                vx = x2 - x1
                vy = y2 - y1
                wx = nx - x1
                wy = ny - y1
                c = max(0, min(1, (wx * vx + wy * vy) / (vx * vx + vy * vy)))
                dx = nx - (x1 + c * vx)
                dy = ny - (y1 + c * vy)
                d = math.sqrt(dx * dx + dy * dy)
                return max(0, min(255, int(255 * (1 - d / width))))

            network = max(
                line_alpha(0.28, 0.67, 0.50, 0.32, 0.035),
                line_alpha(0.50, 0.32, 0.72, 0.67, 0.035),
                line_alpha(0.28, 0.67, 0.72, 0.67, 0.030),
            )
            if alpha and network:
                t = network / 255
                red = int(red * (1 - t) + 112 * t)
                green = int(green * (1 - t) + 232 * t)
                blue = int(blue * (1 - t) + 190 * t)

            for cx, cy, radius in [(0.50, 0.32, 0.105), (0.28, 0.67, 0.105), (0.72, 0.67, 0.105)]:
                d = math.sqrt((nx - cx) ** 2 + (ny - cy) ** 2)
                if alpha and d < radius:
                    red, green, blue = 72, 211, 154
                    if d < radius * 0.45:
                        red, green, blue = 235, 255, 248

            def segment(x1, y1, x2, y2, width):
                vx = x2 - x1
                vy = y2 - y1
                wx = nx - x1
                wy = ny - y1
                c = max(0, min(1, (wx * vx + wy * vy) / (vx * vx + vy * vy)))
                dx = nx - (x1 + c * vx)
                dy = ny - (y1 + c * vy)
                return math.sqrt(dx * dx + dy * dy) < width

            if alpha and (
                segment(0.39, 0.76, 0.50, 0.44, 0.025)
                or segment(0.61, 0.76, 0.50, 0.44, 0.025)
                or segment(0.43, 0.64, 0.57, 0.64, 0.020)
            ):
                red, green, blue = 255, 255, 255

            row += bytes([red, green, blue, alpha])
        rows.append(bytes(row))

    raw = b"".join(rows)

    def chunk(kind, data):
        return (
            struct.pack(">I", len(data))
            + kind
            + data
            + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(raw, 9))
        + chunk(b"IEND", b"")
    )


def ico(images):
    header = struct.pack("<HHH", 0, 1, len(images))
    offset = 6 + 16 * len(images)
    entries = []
    body = b""
    for size, image in images:
        entries.append(
            struct.pack(
                "<BBBBHHII",
                0 if size == 256 else size,
                0 if size == 256 else size,
                0,
                0,
                1,
                32,
                len(image),
                offset,
            )
        )
        body += image
        offset += len(image)
    return header + b"".join(entries) + body


def main():
    root = Path(__file__).resolve().parent.parent
    out = root / "cmd" / "client" / "wix" / "assets" / "anylan.ico"
    images = [(size, png_rgba(size)) for size in (16, 32, 48, 256)]
    out.write_bytes(ico(images))


if __name__ == "__main__":
    main()
