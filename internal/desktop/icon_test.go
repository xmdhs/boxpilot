package desktop

import (
	"bytes"
	"image/png"
	"testing"
)

func TestIconPNG(t *testing.T) {
	for _, size := range []int{1, 16, 32, 128, 256} {
		data := IconPNG(size)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != size || img.Bounds().Dy() != size {
			t.Fatalf("incorrect icon size %v", img.Bounds())
		}
		if !bytes.Equal(data, IconPNG(size)) {
			t.Fatal("icon is nondeterministic")
		}
		if size > 1 {
			_, _, _, cornerAlpha := img.At(0, 0).RGBA()
			_, _, _, centerAlpha := img.At(size/2, size/2).RGBA()
			if cornerAlpha != 0 || centerAlpha == 0 {
				t.Fatal("icon must have transparent corners and visible center")
			}
		}
	}
	if IconPNG(0) != nil || IconPNG(-1) != nil || IconPNG(1025) != nil {
		t.Fatal("out-of-range icon request accepted")
	}
}

func TestTemplateIconIsBlackWithTransparency(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(drawIcon(32, true)))
	if err != nil {
		t.Fatal(err)
	}
	visible, clear := false, false
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if r != 0 || g != 0 || b != 0 {
				t.Fatal("macOS template has colored pixels")
			}
			visible = visible || a > 0
			clear = clear || a == 0
		}
	}
	if !visible || !clear {
		t.Fatal("template has no silhouette")
	}
}
