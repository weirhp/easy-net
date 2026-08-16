from pathlib import Path

from PIL import Image, ImageDraw


ROOT = Path(__file__).resolve().parents[1]
ASSETS = ROOT / "assets"
TRAY = ROOT / "internal" / "tray"


def render_icon(size: int = 1024) -> Image.Image:
    image = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    pixels = image.load()
    start = (139, 92, 246)
    end = (67, 56, 202)
    for y in range(size):
        for x in range(size):
            amount = min(1.0, max(0.0, (x + y) / (size * 2)))
            pixels[x, y] = tuple(round(a + (b - a) * amount) for a, b in zip(start, end)) + (255,)

    mask = Image.new("L", (size, size), 0)
    mask_draw = ImageDraw.Draw(mask)
    margin = round(size * 24 / 512)
    radius = round(size * 116 / 512)
    mask_draw.rounded_rectangle((margin, margin, size - margin, size - margin), radius=radius, fill=255)
    image.putalpha(mask)

    draw = ImageDraw.Draw(image)
    scale = size / 512
    points = [(126, 336), (206, 174), (312, 340), (386, 194)]
    scaled = [(round(x * scale), round(y * scale)) for x, y in points]
    width = round(42 * scale)
    draw.line(scaled, fill="white", width=width, joint="curve")
    radius = round(32 * scale)
    for x, y in scaled:
        draw.ellipse((x - radius, y - radius, x + radius, y + radius), fill="white")
    return image


def main() -> None:
    ASSETS.mkdir(parents=True, exist_ok=True)
    TRAY.mkdir(parents=True, exist_ok=True)
    icon = render_icon()
    png = icon.resize((512, 512), Image.Resampling.LANCZOS)
    png.save(ASSETS / "easy-net-lite.png", optimize=True)
    png.save(TRAY / "easy-net-lite.png", optimize=True)
    sizes = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    icon.save(ASSETS / "easy-net-lite.ico", format="ICO", sizes=sizes)
    icon.save(TRAY / "easy-net-lite.ico", format="ICO", sizes=sizes)


if __name__ == "__main__":
    main()
