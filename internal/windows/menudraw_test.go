//go:build windows && (amd64 || arm64)

package windows

import (
	"testing"
	"unsafe"
)

// The highlights of menu items cover their pixels inside fully, those past
// the rounded corners not at all, and those the corners cross partly.
func TestRoundedCoverage(t *testing.T) {
	r := &rect{0, 0, 40, 20}
	for _, c := range []struct {
		x, y     float64
		min, max float64
	}{
		{20.5, 10.5, 1, 1}, // the middle
		{0.5, 10.5, 1, 1},  // the left edge, between the corners
		{20.5, 0.5, 1, 1},  // the top edge
		{0.5, 0.5, 0, 0},   // past the top-left corner's curve
		{39.5, 19.5, 0, 0}, // past the bottom-right one's
		{1.5, 1.5, 0.01, 0.99},
	} {
		if got := roundedCoverage(c.x, c.y, r, 4); got < c.min || got > c.max {
			t.Errorf("coverage at (%v, %v) = %v, want %v to %v", c.x, c.y, got, c.min, c.max)
		}
	}
}

func TestMenuColors(t *testing.T) {
	if got := dibColor(0x332211); got != 0x112233 {
		t.Errorf("dibColor(0x332211) = %#x, want 0x112233", got)
	}
	if got := blend(0xFFFFFF, 0x000000, 0.5); got != 0x808080 {
		t.Errorf("blend halfway = %#x, want 0x808080", got)
	}
	if got := blend(0x102030, 0xA0B0C0, 0); got != 0xA0B0C0 {
		t.Errorf("blend with none = %#x, want the background", got)
	}
}

// Menus have the material behind them only where DWM surely draws it, and
// WinUI's solid background elsewhere.
func TestMenuGlassConditions(t *testing.T) {
	on := glassConditions{fluent: true, backdrops: true, transparency: true}
	if !on.glass() {
		t.Fatalf("%+v: no material, want it", on)
	}
	for name, off := range map[string]func(*glassConditions){
		"Windows 10":               func(c *glassConditions) { c.fluent = false },
		"Windows 11 before 22H2":   func(c *glassConditions) { c.backdrops = false },
		"a contrast theme":         func(c *glassConditions) { c.contrast = true },
		"transparency effects off": func(c *glassConditions) { c.transparency = false },
		"energy saver":             func(c *glassConditions) { c.energySaver = true },
		"a remote session":         func(c *glassConditions) { c.remote = true },
	} {
		c := on
		off(&c)
		if c.glass() {
			t.Errorf("with %s: the material, want WinUI's solid background", name)
		}
	}
}

// The solid backgrounds are WinUI's fallbacks for its Acrylic, and the
// opaque highlight and separators are its translucent ones over them, as
// they show over the material.
func TestFluentPalette(t *testing.T) {
	near := func(a, b uint32) bool {
		for shift := 0; shift < 24; shift += 8 {
			if d := int(a>>shift&0xFF) - int(b>>shift&0xFF); d < -1 || d > 1 {
				return false
			}
		}
		return true
	}
	for _, c := range []struct {
		dark bool
		back uint32
	}{{false, 0xF9F9F9}, {true, 0x2C2C2C}} {
		p := fluentPalette(c.dark)
		if p.back != c.back {
			t.Errorf("dark %v: background %#06x, want %#06x", c.dark, p.back, c.back)
		}
		if got := blend(dibColor(p.wash.color), dibColor(p.back), p.wash.alpha); !near(got, dibColor(p.plate)) {
			t.Errorf("dark %v: highlight %#06x, want the wash over the background, %#06x", c.dark, p.plate, got)
		}
		if got := blend(dibColor(p.rule.color), dibColor(p.back), p.rule.alpha); !near(got, dibColor(p.line)) {
			t.Errorf("dark %v: separator %#06x, want the rule over the background, %#06x", c.dark, p.line, got)
		}
		if p.border == dwmColorDefault || p.border == p.back {
			t.Errorf("dark %v: outline %#x, want a color of its own", c.dark, p.border)
		}
	}
}

// Items over the material are the premultiplied pixels that show as they
// were drawn over black and over white: WinUI's translucent highlights
// as translucent, text as opaque as it covers, and nothing as nothing.
func TestUnblend(t *testing.T) {
	over := func(c uint32, a float64) (onBlack, onWhite uint32) {
		return blend(c, 0x000000, a), blend(c, 0xFFFFFF, a)
	}
	for _, c := range []struct {
		name  string
		color uint32
		alpha float64
		want  uint32
	}{
		{"nothing", 0x000000, 0, 0x00000000},
		{"opaque text", 0x1A1A1A, 1, 0xFF1A1A1A},
		{"a dark menu's highlight", 0xFFFFFF, 0x0F / 255.0, 0x0F0F0F0F},
		{"a light menu's highlight", 0x000000, 0x09 / 255.0, 0x09000000},
		{"half covered", 0xFFFFFF, 0.5, 0x80808080},
	} {
		k, w := over(c.color, c.alpha)
		if got := unblend(k, w); got != c.want {
			t.Errorf("%s: unblend(%#x, %#x) = %#x, want %#x", c.name, k, w, got, c.want)
		}
	}
}

// A label of a menu item drawn as wide as it measures shows all its ink,
// that past its advance included: Microsoft YaHei UI's, the menu font of
// Chinese Windows, reaches a pixel past it in 粘贴, 删除 and 全选 (#1028).
func TestMenuLabelInk(t *testing.T) {
	dpi := dpiOf(0)
	const face = "Microsoft YaHei UI"
	// In ClearType, as menus draw where it is on, whose ink reaches
	// further than grayscale's.
	const clearTypeQuality = 5
	lf := logFont{Height: -toPx(14, dpi), Weight: 400, Quality: clearTypeQuality}
	copy(lf.FaceName[:], utf16z(face))
	text, _, _ := procCreateFontIndirectW.Call(uintptr(unsafe.Pointer(&lf)))
	defer procDeleteObject.Call(text)
	if !fontFace(text, face) {
		t.Skip(face + " is not installed")
	}
	glyphs := createFont(-toPx(menuGlyphSize, dpi), "Segoe UI")
	defer procDeleteObject.Call(glyphs)
	b := &Backend{themeSource: "light", menuFonts: map[menuFontKey]menuFonts{{dpi: dpi}: {text: text, glyphs: glyphs}}}
	p := b.menuPalette()
	back := dibColor(p.back)
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	for _, label := range []string{"撤销", "剪切", "复制", "粘贴", "删除", "全选", "查看歌手：房东的猫"} {
		// Its ink drawn with room around it.
		const pad = 50
		w := textWidth(screen, text, label) + 2*pad
		h := toPx(menuItemHeight, dpi)
		img := newBitmap(screen, w, h)
		for i := range img.px {
			img.px[i] = back
		}
		old, _, _ := procSelectObject.Call(img.dc, text)
		procSetBkMode.Call(img.dc, 1) // TRANSPARENT
		procSetTextColor.Call(img.dc, uintptr(p.text))
		at := rect{pad, 0, w, h}
		procDrawTextW.Call(img.dc, uintptr(unsafe.Pointer(u16(label))), ^uintptr(0), uintptr(unsafe.Pointer(&at)), dtSingleLine|dtVCenter|dtNoClip)
		procGdiFlush.Call()
		procSelectObject.Call(img.dc, old)
		want := inkLast(img, w, back)
		want -= pad
		img.free()

		// The item, as wide as it needs.
		l := &menuLook{label: label}
		w = b.itemWidth(l, dpi)
		img = newBitmap(screen, w, h)
		b.drawItem(0, l, &drawItemStruct{CtlType: odtMenu, DC: img.dc, Item: rect{0, 0, w, h}})
		procGdiFlush.Call()
		got := inkLast(img, w, back)
		got -= toPx(menuInset+menuPadding, dpi)
		img.free()
		if want < 0 || got != want {
			t.Errorf("%q: the item shows its ink up to column %d of the label, want %d", label, got, want)
		}
	}
}

// inkLast returns the last column of a bitmap w pixels wide holding
// pixels other than back, -1 for none.
func inkLast(img *bitmap, w int32, back uint32) int32 {
	last := int32(-1)
	for i, p := range img.px {
		if p&0xFFFFFF != back {
			last = max(last, int32(i)%w)
		}
	}
	return last
}
