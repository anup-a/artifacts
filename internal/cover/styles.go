package cover

import "strings"

// Pattern opacities stay between 6 and 10 percent so the background never
// competes with the title.
const (
	dotOpacity  = "0.10"
	lineOpacity = "0.07"
	ringOpacity = "0.07"
	// The wash carries the project colour as a corner-anchored bloom that fades
	// across most of the card. It is a hint, not a tint: at 0.12 a gallery of
	// covers read as pastel, so the dashboard's one colour mark per item is
	// the dot beside its name, not the cover ground.
	washOpacity  = "0.04"
	dotPitch     = 24
	gridPitch    = 48
	linePitch    = 28
	ringStep     = 60
	ringCount    = 14
	ringCenterX  = Width + 60
	ringCenterY  = Height + 60
	washCenterX  = Width
	washCenterY  = 0
	washRadius   = 1080
	patternUnits = `patternUnits="userSpaceOnUse"`
)

// drawWash tints the top-right corner with the accent colour, fading across
// most of the card. It runs under every pattern when the card has a project,
// so project cards always carry their colour and only no-project cards are
// purely grey.
func drawWash(b *strings.Builder, accent string) {
	b.WriteString(`<defs><radialGradient id="w"><stop offset="0" stop-color="` + accent + `" stop-opacity="` + washOpacity + `"/><stop offset="1" stop-color="` + accent + `" stop-opacity="0"/></radialGradient></defs>`)
	b.WriteString(`<circle cx="` + f(washCenterX) + `" cy="` + f(washCenterY) + `" r="` + f(washRadius) + `" fill="url(#w)"/>`)
}

// drawPattern writes the background pattern for a style over the ground rect.
func drawPattern(b *strings.Builder, style, accent string) {
	switch style {
	case "dots":
		b.WriteString(`<defs><pattern id="p" width="` + f(dotPitch) + `" height="` + f(dotPitch) + `" ` + patternUnits + `>` +
			`<circle class="pf" cx="` + f(dotPitch/2) + `" cy="` + f(dotPitch/2) + `" r="1.6" fill="` + foreground + `" opacity="` + dotOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "grid":
		b.WriteString(`<defs><pattern id="p" width="` + f(gridPitch) + `" height="` + f(gridPitch) + `" ` + patternUnits + `>` +
			`<path class="ps" d="M` + f(gridPitch) + ` 0H0V` + f(gridPitch) + `" fill="none" stroke="` + foreground + `" stroke-width="1" opacity="` + lineOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "lines":
		b.WriteString(`<defs><pattern id="p" width="` + f(linePitch) + `" height="` + f(linePitch) + `" ` + patternUnits + ` patternTransform="rotate(-45)">` +
			`<path class="ps" d="M0 0V` + f(linePitch) + `" stroke="` + foreground + `" stroke-width="1" opacity="` + lineOpacity + `"/></pattern></defs>`)
		b.WriteString(`<rect width="1200" height="630" fill="url(#p)"/>`)
	case "wash", "plain":
		// The wash itself is drawn by drawWash for every card with a project;
		// as a style name it just means "no pattern on top".
	case "rings":
		for i := 1; i <= ringCount; i++ {
			b.WriteString(`<circle class="ps" cx="` + f(ringCenterX) + `" cy="` + f(ringCenterY) + `" r="` + f(float64(i*ringStep)) + `" fill="none" stroke="` + foreground + `" stroke-width="1" opacity="` + ringOpacity + `"/>`)
		}
	}
}
