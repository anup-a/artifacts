package cover

import "strings"

// Pattern opacities stay between 6 and 10 percent so the background never
// competes with the title.
const (
	dotOpacity   = "0.10"
	lineOpacity  = "0.07"
	ringOpacity  = "0.07"
	washOpacity  = "0.08"
	dotPitch     = 24
	gridPitch    = 48
	linePitch    = 28
	ringStep     = 60
	ringCount    = 14
	ringCenterX  = Width + 60
	ringCenterY  = Height + 60
	washCenterX  = Width - 120
	washCenterY  = 80
	washRadius   = 620
	patternUnits = `patternUnits="userSpaceOnUse"`
)

// drawPattern writes the background for a style over the ground rect.
func drawPattern(b *strings.Builder, style, accent string) {
	switch style {
	case "dots":
		b.WriteString(`<defs><pattern id="p" width="` + f(dotPitch) + `" height="` + f(dotPitch) + `" ` + patternUnits + `>` +
			`<circle cx="` + f(dotPitch/2) + `" cy="` + f(dotPitch/2) + `" r="1.6" fill="` + foreground + `" opacity="` + dotOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "grid":
		b.WriteString(`<defs><pattern id="p" width="` + f(gridPitch) + `" height="` + f(gridPitch) + `" ` + patternUnits + `>` +
			`<path d="M` + f(gridPitch) + ` 0H0V` + f(gridPitch) + `" fill="none" stroke="` + foreground + `" stroke-width="1" opacity="` + lineOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "lines":
		b.WriteString(`<defs><pattern id="p" width="` + f(linePitch) + `" height="` + f(linePitch) + `" ` + patternUnits + ` patternTransform="rotate(-45)">` +
			`<path d="M0 0V` + f(linePitch) + `" stroke="` + foreground + `" stroke-width="1" opacity="` + lineOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "wash":
		b.WriteString(`<defs><radialGradient id="w"><stop offset="0" stop-color="` + accent + `" stop-opacity="` + washOpacity + `"/><stop offset="1" stop-color="` + accent + `" stop-opacity="0"/></radialGradient></defs>`)
		b.WriteString(`<circle cx="` + f(washCenterX) + `" cy="` + f(washCenterY) + `" r="` + f(washRadius) + `" fill="url(#w)"/>`)
	case "rings":
		for i := 1; i <= ringCount; i++ {
			b.WriteString(`<circle cx="` + f(ringCenterX) + `" cy="` + f(ringCenterY) + `" r="` + f(float64(i*ringStep)) + `" fill="none" stroke="` + foreground + `" stroke-width="1" opacity="` + ringOpacity + `"/>`)
		}
	case "plain":
	}
}
