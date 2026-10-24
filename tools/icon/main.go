// Command icon draws Stilday's app icon: a snail, because the app is about a
// quiet day that leaves one line behind, not about its features. Everything is
// vector, so every size macOS asks for comes from one description.
//
//	go run ./tools/icon            # writes resources/icon.png and icon-dev.png
//
// The tile keeps Apple's icon grid: an 824 squircle centred on 1024, corner
// radius 185, nothing baked behind it.
package main

import (
	"flag"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/image/vector"
)

type rgb struct{ r, g, b float64 }

func hex(s string) rgb {
	n := func(i int) float64 {
		var v float64
		for _, c := range s[i : i+2] {
			v *= 16
			switch {
			case c >= '0' && c <= '9':
				v += float64(c - '0')
			case c >= 'a' && c <= 'f':
				v += float64(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v += float64(c-'A') + 10
			}
		}
		return v
	}
	return rgb{n(1), n(3), n(5)}
}

func (c rgb) with(a float64) color.NRGBA {
	return color.NRGBA{uint8(math.Round(c.r)), uint8(math.Round(c.g)), uint8(math.Round(c.b)), uint8(math.Round(a * 255))}
}

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

type canvas struct {
	img  *image.RGBA
	size int
	k    float64
}

func newCanvas(size int) *canvas {
	return &canvas{img: image.NewRGBA(image.Rect(0, 0, size, size)), size: size, k: float64(size) / 1024}
}

type stop struct {
	p float64
	c rgb
}

func shade(stops []stop, t float64) rgb {
	if t <= stops[0].p {
		return stops[0].c
	}
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].p {
			span := stops[i].p - stops[i-1].p
			if span <= 0 {
				return stops[i].c
			}
			return mix(stops[i-1].c, stops[i].c, (t-stops[i-1].p)/span)
		}
	}
	return stops[len(stops)-1].c
}

func (c *canvas) vstops(stops []stop) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, c.size, c.size))
	row := image.NewRGBA(image.Rect(0, 0, c.size, 1))
	for y := 0; y < c.size; y++ {
		col := shade(stops, float64(y)/float64(c.size-1)).with(1)
		draw.Draw(row, row.Bounds(), image.NewUniform(col), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(0, y, c.size, y+1), row, image.Point{}, draw.Src)
	}
	return img
}

func (c *canvas) radial(cx, cy, r float64, inner, outer color.NRGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, c.size, c.size))
	for y := 0; y < c.size; y++ {
		fy := (float64(y)+0.5)/c.k - cy
		for x := 0; x < c.size; x++ {
			fx := (float64(x)+0.5)/c.k - cx
			d := math.Hypot(fx, fy) / r
			if d >= 1 {
				continue
			}
			t := d * d
			img.Set(x, y, color.NRGBA{
				R: uint8(math.Round(float64(inner.R) + (float64(outer.R)-float64(inner.R))*t)),
				G: uint8(math.Round(float64(inner.G) + (float64(outer.G)-float64(inner.G))*t)),
				B: uint8(math.Round(float64(inner.B) + (float64(outer.B)-float64(inner.B))*t)),
				A: uint8(math.Round(float64(inner.A) + (float64(outer.A)-float64(inner.A))*t)),
			})
		}
	}
	return img
}

func (c *canvas) fill(build func(*vector.Rasterizer), src image.Image) {
	c.composite(src, c.mask(build))
}

func (c *canvas) composite(src image.Image, m *image.Alpha) {
	draw.DrawMask(c.img, c.img.Bounds(), src, image.Point{}, m, image.Point{}, draw.Over)
}

func (c *canvas) solid(build func(*vector.Rasterizer), col color.NRGBA) {
	c.fill(build, image.NewUniform(col))
}

func (c *canvas) mask(build func(*vector.Rasterizer)) *image.Alpha {
	r := vector.NewRasterizer(c.size, c.size)
	r.DrawOp = draw.Src
	build(r)
	m := image.NewAlpha(c.img.Bounds())
	r.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

func blur(m *image.Alpha, radius int) {
	if radius <= 0 {
		return
	}
	w, h := m.Bounds().Dx(), m.Bounds().Dy()
	buf := make([]float64, w*h)
	for i, v := range m.Pix {
		buf[i] = float64(v)
	}
	tmp := make([]float64, w*h)
	pass := func(src, dst []float64, rows bool) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				var sum float64
				var n int
				for d := -radius; d <= radius; d++ {
					sx, sy := x, y
					if rows {
						sx += d
					} else {
						sy += d
					}
					if sx < 0 || sx >= w || sy < 0 || sy >= h {
						continue
					}
					sum += src[sy*w+sx]
					n++
				}
				dst[y*w+x] = sum / float64(n)
			}
		}
	}
	for range 2 {
		pass(buf, tmp, true)
		pass(tmp, buf, false)
	}
	for i := range m.Pix {
		m.Pix[i] = uint8(math.Round(buf[i]))
	}
}

func (c *canvas) shadow(build func(*vector.Rasterizer), dx, dy, radius, alpha float64, tint rgb) {
	m := c.mask(build)
	sx := int(math.Round(dx * c.k))
	sy := int(math.Round(dy * c.k))
	if sx != 0 || sy != 0 {
		src := image.NewAlpha(c.img.Bounds())
		draw.Draw(src, image.Rect(sx, sy, c.size+sx, c.size+sy), m, image.Point{}, draw.Src)
		m = src
	}
	blur(m, int(math.Round(radius*c.k)))
	draw.DrawMask(c.img, c.img.Bounds(), image.NewUniform(tint.with(alpha)), image.Point{}, m, image.Point{}, draw.Over)
}

func (c *canvas) squircle(cx, cy, half, n float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		const steps = 320
		for i := 0; i <= steps; i++ {
			t := 2 * math.Pi * float64(i) / steps
			ct, st := math.Cos(t), math.Sin(t)
			x := cx + half*math.Copysign(math.Pow(math.Abs(ct), 2/n), ct)
			y := cy + half*math.Copysign(math.Pow(math.Abs(st), 2/n), st)
			if i == 0 {
				r.MoveTo(float32(x*c.k), float32(y*c.k))
			} else {
				r.LineTo(float32(x*c.k), float32(y*c.k))
			}
		}
		r.ClosePath()
	}
}

func (c *canvas) roundRect(x0, y0, x1, y1, rad float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		x0, y0, x1, y1, rad := x0*c.k, y0*c.k, x1*c.k, y1*c.k, rad*c.k
		r.MoveTo(float32(x0+rad), float32(y0))
		r.LineTo(float32(x1-rad), float32(y0))
		r.QuadTo(float32(x1), float32(y0), float32(x1), float32(y0+rad))
		r.LineTo(float32(x1), float32(y1-rad))
		r.QuadTo(float32(x1), float32(y1), float32(x1-rad), float32(y1))
		r.LineTo(float32(x0+rad), float32(y1))
		r.QuadTo(float32(x0), float32(y1), float32(x0), float32(y1-rad))
		r.LineTo(float32(x0), float32(y0+rad))
		r.QuadTo(float32(x0), float32(y0), float32(x0+rad), float32(y0))
		r.ClosePath()
	}
}

func (c *canvas) circle(cx, cy, rad float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		cx, cy, rad := cx*c.k, cy*c.k, rad*c.k
		const steps = 200
		for i := 0; i <= steps; i++ {
			t := 2 * math.Pi * float64(i) / steps
			x, y := cx+rad*math.Cos(t), cy+rad*math.Sin(t)
			if i == 0 {
				r.MoveTo(float32(x), float32(y))
			} else {
				r.LineTo(float32(x), float32(y))
			}
		}
		r.ClosePath()
	}
}

func arc(r *vector.Rasterizer, cx, cy, rad, a0, a1, k float64) {
	n := int(math.Ceil(math.Abs(a1-a0) / (math.Pi / 2)))
	d := (a1 - a0) / float64(n)
	kc := 4.0 / 3.0 * math.Tan(d/4)
	a := a0
	for i := 0; i < n; i++ {
		p0x, p0y := cx+rad*math.Cos(a), cy+rad*math.Sin(a)
		a2 := a + d
		p3x, p3y := cx+rad*math.Cos(a2), cy+rad*math.Sin(a2)
		c1x, c1y := p0x-kc*rad*math.Sin(a), p0y+kc*rad*math.Cos(a)
		c2x, c2y := p3x+kc*rad*math.Sin(a2), p3y-kc*rad*math.Cos(a2)
		r.CubeTo(float32(c1x*k), float32(c1y*k), float32(c2x*k), float32(c2y*k), float32(p3x*k), float32(p3y*k))
		a = a2
	}
}

// capsule is a thick round-capped stroke. Both caps have to start where the
// pen already is, or the path cuts across and the shape comes out as a sliver.
func (c *canvas) capsule(x1, y1, x2, y2, w float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		ang := math.Atan2(y2-y1, x2-x1)
		nx, ny := -math.Sin(ang), math.Cos(ang)
		h := w / 2
		r.MoveTo(float32((x1+nx*h)*c.k), float32((y1+ny*h)*c.k))
		r.LineTo(float32((x2+nx*h)*c.k), float32((y2+ny*h)*c.k))
		arc(r, x2, y2, h, ang+math.Pi/2, ang-math.Pi/2, c.k)
		r.LineTo(float32((x1-nx*h)*c.k), float32((y1-ny*h)*c.k))
		arc(r, x1, y1, h, ang-math.Pi/2, ang+math.Pi/2, c.k)
		r.ClosePath()
	}
}

// spiralShell is an Archimedean spiral band that tapers outward, the way a
// snail's whorl does.
func (c *canvas) spiralShell(cx, cy, a, b, w, turns, taper, phase float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		const steps = 900
		t1 := 2 * math.Pi * turns
		width := func(t float64) float64 { return w * (1 - taper*t/t1) }
		for i := 0; i <= steps; i++ {
			t := t1 * float64(i) / steps
			rad := a + b*t + width(t)/2
			x, y := cx+rad*math.Cos(t+phase), cy+rad*math.Sin(t+phase)
			if i == 0 {
				r.MoveTo(float32(x*c.k), float32(y*c.k))
			} else {
				r.LineTo(float32(x*c.k), float32(y*c.k))
			}
		}
		for i := steps; i >= 0; i-- {
			t := t1 * float64(i) / steps
			rad := a + b*t - width(t)/2
			r.LineTo(float32((cx+rad*math.Cos(t+phase))*c.k), float32((cy+rad*math.Sin(t+phase))*c.k))
		}
		r.ClosePath()
	}
}

// ring is one growth ring: an annulus whose radius wobbles, so it reads as
// wood and not as a target.
func (c *canvas) ring(cx, cy, rad, w, amp, phase float64) func(*vector.Rasterizer) {
	edge := func(t float64, half float64) float64 {
		return rad + half + amp*math.Sin(3*t+phase) + 0.45*amp*math.Sin(7*t+2*phase)
	}
	return func(r *vector.Rasterizer) {
		const steps = 480
		for i := 0; i <= steps; i++ {
			t := 2 * math.Pi * float64(i) / steps
			rr := edge(t, w/2)
			x, y := cx+rr*math.Cos(t), cy+rr*math.Sin(t)
			if i == 0 {
				r.MoveTo(float32(x*c.k), float32(y*c.k))
			} else {
				r.LineTo(float32(x*c.k), float32(y*c.k))
			}
		}
		for i := steps; i >= 0; i-- {
			t := 2 * math.Pi * float64(i) / steps
			rr := edge(t, -w/2)
			r.LineTo(float32((cx+rr*math.Cos(t))*c.k), float32((cy+rr*math.Sin(t))*c.k))
		}
		r.ClosePath()
	}
}

func (c *canvas) write(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, c.img)
}

// ------------------------------------------------------------------- 蜗牛
//
// The silhouette follows the shape language of the Fluent Emoji and Twemoji
// snails — a big shell, an S-curved neck, two club-tipped tentacles — because
// that shape survives 16px. The palette and the material are ours.

// spine is one control point of a tapered stroke: a position and a half width.
type spine struct{ x, y, w float64 }

// stroke builds a smooth, tapered outline around a spine, which is how the
// snail's body and tentacles get drawn: procedural circles kept going lumpy.
func (c *canvas) stroke(pts []spine) func(*vector.Rasterizer) {
	at := func(i int) spine {
		if i < 0 {
			i = 0
		}
		if i >= len(pts) {
			i = len(pts) - 1
		}
		return pts[i]
	}
	// Catmull-Rom through the middle of each span, with the width eased too.
	point := func(i int, t float64) spine {
		p0, p1, p2, p3 := at(i-1), at(i), at(i+1), at(i+2)
		t2, t3 := t*t, t*t*t
		cr := func(a, b, cc, d float64) float64 {
			return 0.5 * (2*b + (-a+cc)*t + (2*a-5*b+4*cc-d)*t2 + (-a+3*b-3*cc+d)*t3)
		}
		return spine{
			x: cr(p0.x, p1.x, p2.x, p3.x),
			y: cr(p0.y, p1.y, p2.y, p3.y),
			w: cr(p0.w, p1.w, p2.w, p3.w),
		}
	}
	return func(r *vector.Rasterizer) {
		const per = 14
		type pt struct{ x, y float64 }
		var left, right []pt
		for i := 0; i+1 < len(pts); i++ {
			for k := 0; k < per; k++ {
				t := float64(k) / per
				p := point(i, t)
				q := point(i, t+0.01)
				dx, dy := q.x-p.x, q.y-p.y
				n := math.Hypot(dx, dy)
				if n == 0 {
					n = 1
				}
				nx, ny := -dy/n*p.w, dx/n*p.w
				left = append(left, pt{p.x + nx, p.y + ny})
				right = append(right, pt{p.x - nx, p.y - ny})
			}
		}
		last := pts[len(pts)-1]
		left = append(left, pt{last.x, last.y})
		right = append(right, pt{last.x, last.y})
		for i, p := range left {
			if i == 0 {
				r.MoveTo(float32(p.x*c.k), float32(p.y*c.k))
			} else {
				r.LineTo(float32(p.x*c.k), float32(p.y*c.k))
			}
		}
		for i := len(right) - 1; i >= 0; i-- {
			p := right[i]
			r.LineTo(float32(p.x*c.k), float32(p.y*c.k))
		}
		r.ClosePath()
	}
}

// band is an annulus between two radii: a ring, used for the shell's rim. The
// inner circle winds the other way, which is what makes it a hole.
func (c *canvas) band(cx, cy, ro, ri float64) func(*vector.Rasterizer) {
	return func(r *vector.Rasterizer) {
		r.MoveTo(float32((cx+ro)*c.k), float32(cy*c.k))
		arc(r, cx, cy, ro, 0, 2*math.Pi, c.k)
		r.LineTo(float32((cx+ri)*c.k), float32(cy*c.k))
		arc(r, cx, cy, ri, 2*math.Pi, 0, c.k)
		r.ClosePath()
	}
}

// palette is one colourway of the snail: tile light, shell, band, ink.
type palette struct {
	name  string
	tile  []stop
	body  rgb
	shell rgb
	band  rgb
	dot   rgb
	ball  rgb
	pupil rgb
}

// mark draws the snail on transparent canvas, so it can be shifted as one piece.
func (c *canvas) mark(size int, p palette) *canvas {
	s := newCanvas(size)
	ink := p.body.with(1)

	// Proportions measured off the generated reference: shell is ~40% of the
	// mark, head much smaller than the foot.
	body := s.stroke([]spine{
		{330, 486, 86},
		{332, 560, 84},
		{356, 640, 80},
		{430, 692, 62},
		{560, 716, 50},
		{690, 726, 44},
		{790, 730, 20},
	})
	s.shadow(body, 0, 14, 24, 0.13, hex("#7A5A3C"))
	s.solid(body, ink)
	s.solid(s.circle(330, 486, 86), ink)
	s.solid(s.circle(790, 730, 20), ink)
	// A short rounded snout, and one eye: what makes it an animal.
	s.solid(s.capsule(322, 506, 224, 518, 46), ink)
	s.solid(s.circle(320, 492, 15), p.pupil.with(1))

	// Two thin antennae with pale balls.
	s.solid(s.capsule(314, 444, 300, 316, 17), ink)
	s.solid(s.capsule(372, 450, 398, 322, 17), ink)
	s.solid(s.circle(294, 306, 36), p.ball.with(1))
	s.solid(s.circle(404, 314, 36), p.ball.with(1))

	// Shell: solid disc, one bold band of pale shell spiralled off-centre (a
	// spiral centred in its disc reads as a bullseye), the eye, and a rim ring
	// that hides where the band leaves the disc.
	shx, shy, shr := 594.0, 486.0, 222.0
	disc := func(r float64) func(*vector.Rasterizer) { return s.circle(shx, shy, r) }
	s.solid(disc(shr), p.shell.with(1))
	band := newCanvas(size)
	band.solid(s.spiralShell(shx+26, shy+34, 0, 26, 72, 1.55, 0.18, 2.2), p.band.with(1))
	s.composite(band.img, s.mask(disc(shr)))
	s.solid(s.band(shx, shy, shr, shr-34), p.shell.with(1))
	s.solid(s.circle(shx+26, shy+34, 52), p.band.with(1))
	s.solid(s.circle(shx+26, shy+34, 24), p.dot.with(1))
	return s
}

func snail(size int, p palette) *canvas {
	c := newCanvas(size)
	c.fill(c.squircle(512, 512, 412, 5), c.vstops(p.tile))
	// The mark was optically left-heavy: shift the whole piece, not its parts.
	drift := 18.0
	m := c.mark(size, p)
	shift := int(math.Round(drift * c.k))
	draw.Draw(c.img, image.Rect(shift, 0, shift+c.size, c.size), m.img, image.Point{}, draw.Over)
	return c
}

var (
	// graphite is the shipped icon.
	graphite = palette{
		tile:  []stop{{0.00, hex("#F4F2ED")}, {0.55, hex("#E4E0D8")}, {1.00, hex("#D2CDC2")}},
		body:  hex("#2B2B2E"),
		shell: hex("#4A4A50"),
		band:  hex("#EDE6D6"),
		dot:   hex("#3F3F45"),
		ball:  hex("#FAF4E6"),
		pupil: hex("#1C1C1F"),
	}
	// dev is the same snail in the app's accent blue, so a development build
	// is obvious in the Dock without a badge or a second icon shape.
	dev = palette{
		tile:  []stop{{0.00, hex("#F4F2ED")}, {0.55, hex("#E4E0D8")}, {1.00, hex("#D2CDC2")}},
		body:  hex("#2B2B2E"),
		shell: hex("#0A84FF"),
		band:  hex("#EAF2FC"),
		dot:   hex("#0866C4"),
		ball:  hex("#FAF4E6"),
		pupil: hex("#1C1C1F"),
	}
)

func main() {
	out := flag.String("out", "resources", "directory for icon.png and icon-dev.png")
	flag.Parse()
	for _, v := range []struct {
		name string
		pal  palette
	}{
		{"icon.png", graphite},
		{"icon-dev.png", dev},
	} {
		path := filepath.Join(*out, v.name)
		if err := snail(1024, v.pal).write(path); err != nil {
			log.Fatalf("icon: %v", err)
		}
	}
}
