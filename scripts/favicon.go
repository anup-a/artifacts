//go:build ignore

// favicon rasterizes the shelf mark (internal/app/static/favicon.svg) into the
// PNG and ICO files search engines and older browsers ask for. Google shows a
// generic globe for a site whose only icon is an SVG, so these ship too.
// Usage: go run ./scripts/favicon.go
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// rect is one bar of the mark in the SVG's 16x16 space, with its rotation
// in degrees about (cx, cy).
type rect struct {
	x, y, w, h, r float64
	rot, cx, cy   float64
}

var mark = []rect{
	{x: 1.5, y: 12.6, w: 13, h: 1.8, r: .9},
	{x: 2.9, y: 4.6, w: 2.2, h: 8.1, r: .6},
	{x: 6.3, y: 3, w: 3, h: 9.7, r: .7},
	{x: 10.5, y: 5.6, w: 2.2, h: 7.4, r: .6, rot: 11, cx: 11.6, cy: 13},
}

var (
	ink   = color.NRGBA{0x17, 0x17, 0x17, 0xff}
	paper = color.NRGBA{0xff, 0xff, 0xff, 0xff}
)

// inside reports whether (px, py), in mark space, falls in the rounded rect.
func (q rect) inside(px, py float64) bool {
	if q.rot != 0 {
		a := -q.rot * math.Pi / 180
		dx, dy := px-q.cx, py-q.cy
		px = q.cx + dx*math.Cos(a) - dy*math.Sin(a)
		py = q.cy + dx*math.Sin(a) + dy*math.Cos(a)
	}
	// Distance to a rounded box, the usual way.
	hx, hy := q.w/2-q.r, q.h/2-q.r
	dx := math.Abs(px-(q.x+q.w/2)) - hx
	dy := math.Abs(py-(q.y+q.h/2)) - hy
	ox, oy := math.Max(dx, 0), math.Max(dy, 0)
	return math.Hypot(ox, oy)+math.Min(math.Max(dx, dy), 0) <= q.r
}

// render draws the mark on a white rounded tile, so it reads on both the
// light and the dark result pages; pad is the margin in mark units.
func render(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersamples per axis
	const pad = 2.5
	scale := float64(size) / (16 + 2*pad)
	corner := float64(size) * 0.22
	tile := rect{x: 0, y: 0, w: float64(size), h: float64(size), r: corner}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var inTile, inMark int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := float64(x) + (float64(sx)+.5)/ss
					fy := float64(y) + (float64(sy)+.5)/ss
					if !tile.inside(fx, fy) {
						continue
					}
					inTile++
					mx, my := fx/scale-pad, fy/scale-pad
					for _, q := range mark {
						if q.inside(mx, my) {
							inMark++
							break
						}
					}
				}
			}
			if inTile == 0 {
				continue
			}
			t := float64(inMark) / float64(inTile)
			mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
			img.SetNRGBA(x, y, color.NRGBA{mix(paper.R, ink.R), mix(paper.G, ink.G), mix(paper.B, ink.B), uint8(math.Round(255 * float64(inTile) / ss / ss))})
		}
	}
	return img
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// ico packs PNG images into one .ico, which every browser since Vista reads.
func ico(sizes ...int) []byte {
	var head, body bytes.Buffer
	binary.Write(&head, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for _, s := range sizes {
		p := encode(render(s))
		dim := uint8(s)
		if s >= 256 {
			dim = 0
		}
		head.Write([]byte{dim, dim, 0, 0})
		binary.Write(&head, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&head, binary.LittleEndian, [2]uint32{uint32(len(p)), uint32(offset)})
		offset += len(p)
		body.Write(p)
	}
	return append(head.Bytes(), body.Bytes()...)
}

func main() {
	dir := "internal/app/static/"
	write := func(name string, b []byte) {
		if err := os.WriteFile(dir+name, b, 0o644); err != nil {
			panic(err)
		}
	}
	write("favicon-48.png", encode(render(48)))
	write("favicon-192.png", encode(render(192)))
	write("apple-touch-icon.png", encode(render(180)))
	write("favicon.ico", ico(16, 32, 48))
}
