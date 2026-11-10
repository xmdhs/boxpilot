package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// IconPNG draws BoxPilot's independent compass/route icon entirely in Go.
// Main may write these bytes to resources/icon.png; no installed icon theme or
// external asset is required. Sizes outside 1..1024 return nil.
func IconPNG(size int) []byte { return drawIcon(size, false) }

func trayIconPNG(size int) []byte { return drawIcon(size, runtime.GOOS == "darwin") }

func drawIcon(size int, template bool) []byte {
	if size < 1 || size > 1024 {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	// Supersample the normalized geometry without allocating a large
	// intermediate image, retaining antialiasing on small tray icons.
	const samples = 4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var red, green, blue, alpha int
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := (float64(x)+(float64(sx)+0.5)/samples)/float64(size)*2 - 1
					py := (float64(y)+(float64(sy)+0.5)/samples)/float64(size)*2 - 1
					c := iconPixel(px, py, template)
					red += int(c.R) * int(c.A)
					green += int(c.G) * int(c.A)
					blue += int(c.B) * int(c.A)
					alpha += int(c.A)
				}
			}
			if alpha > 0 {
				img.SetNRGBA(x, y, color.NRGBA{R: uint8(red / alpha), G: uint8(green / alpha), B: uint8(blue / alpha), A: uint8(alpha / (samples * samples))})
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil
	}
	return b.Bytes()
}

func iconPixel(x, y float64, template bool) color.NRGBA {
	r := math.Hypot(x, y)
	transparent := color.NRGBA{}
	blue := color.NRGBA{R: 53, G: 105, B: 225, A: 255}
	white := color.NRGBA{R: 245, G: 250, B: 255, A: 255}
	mint := color.NRGBA{R: 72, G: 224, B: 180, A: 255}
	if r > 0.88 {
		return transparent
	}
	// A circular compass, a north-east needle and its south-west tail.
	needle := inTriangle(x, y, -0.16, -0.06, 0.42, -0.48, 0.10, 0.18)
	tail := inTriangle(x, y, -0.16, -0.06, -0.35, 0.40, 0.10, 0.18)
	if template {
		if r >= 0.69 || needle || tail {
			return color.NRGBA{A: 255}
		}
		return transparent
	}
	if needle {
		return white
	}
	if tail || math.Hypot(x, y+0.65) < 0.065 {
		return mint
	}
	return blue
}

func inTriangle(x, y, ax, ay, bx, by, cx, cy float64) bool {
	cross := func(px, py, qx, qy float64) float64 { return (x-qx)*(py-qy) - (px-qx)*(y-qy) }
	a, b, c := cross(ax, ay, bx, by), cross(bx, by, cx, cy), cross(cx, cy, ax, ay)
	return (a >= 0 && b >= 0 && c >= 0) || (a <= 0 && b <= 0 && c <= 0)
}
