package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

// ---- Fast painting ----
// lipgloss.Style.Render is too heavy for single cells. For bars and graphs, which have many cells,
// build the escape sequence for each color combination once and reuse it.

type pen struct {
	fg, bg string // "#RRGGBB" or empty
	bold   bool
}

var (
	penCache = map[pen]string{}
	prof     termenv.Profile
	profSet  bool
)

func hexOf(c lipgloss.TerminalColor) string {
	switch v := c.(type) {
	case lipgloss.Color:
		return string(v)
	case lipgloss.AdaptiveColor:
		if lipgloss.HasDarkBackground() {
			return v.Dark
		}
		return v.Light
	}
	return ""
}

func paint(p pen, s string) string {
	pre, ok := penCache[p]
	if !ok {
		if !profSet {
			prof, profSet = lipgloss.ColorProfile(), true
		}
		var seq []string
		if p.bold && prof != termenv.Ascii {
			seq = append(seq, "1")
		}
		if prof != termenv.Ascii {
			if p.fg != "" {
				seq = append(seq, prof.Color(p.fg).Sequence(false))
			}
			if p.bg != "" {
				seq = append(seq, prof.Color(p.bg).Sequence(true))
			}
		}
		if len(seq) > 0 {
			pre = "\x1b[" + strings.Join(seq, ";") + "m"
		}
		penCache[p] = pre
	}
	if pre == "" || s == "" {
		return s
	}
	return pre + s + "\x1b[0m"
}

func fgPen(c lipgloss.TerminalColor) pen { return pen{fg: hexOf(c)} }

var (
	cAccent = lipgloss.Color("#C4A7E7")
	cText   = lipgloss.AdaptiveColor{Light: "#2E3440", Dark: "#E4E4F0"}
	cMuted  = lipgloss.AdaptiveColor{Light: "#6B7089", Dark: "#9A9BB8"}
	cBorder = lipgloss.AdaptiveColor{Light: "#C9CCDA", Dark: "#55597A"}
	cTrack  = lipgloss.AdaptiveColor{Light: "#DADDE8", Dark: "#45496A"}

	cPCore = lipgloss.Color("#82AAFF")
	cECore = lipgloss.Color("#C792EA")

	cApp        = lipgloss.Color("#82AAFF")
	cWired      = lipgloss.Color("#FFCB6B")
	cCompressed = lipgloss.Color("#C792EA")
	cCached     = lipgloss.Color("#6C7394")

	cOK   = lipgloss.Color("#C3E88D")
	cWarn = lipgloss.Color("#FFCB6B")
	cBad  = lipgloss.Color("#F07178")

	sText   = lipgloss.NewStyle().Foreground(cText)
	sMuted  = lipgloss.NewStyle().Foreground(cMuted)
	sBorder = lipgloss.NewStyle().Foreground(cBorder)
	sTitle  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sBold   = lipgloss.NewStyle().Foreground(cText).Bold(true)
)

// Precompute a 101-step color ramp for 0% to 100%.
var grad [101]lipgloss.Color

func init() {
	hexes := []string{"#89DDFF", "#C3E88D", "#FFCB6B", "#F07178"} // sky -> lime -> amber -> coral
	stops := make([]colorful.Color, len(hexes))
	for i, h := range hexes {
		stops[i], _ = colorful.Hex(h)
	}
	for i := 0; i <= 100; i++ {
		seg := float64(i) / 100 * float64(len(stops)-1)
		k := int(seg)
		if k >= len(stops)-1 {
			k = len(stops) - 2
		}
		c := stops[k].BlendLab(stops[k+1], seg-float64(k)).Clamped()
		grad[i] = lipgloss.Color(c.Hex())
	}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func gradAt(t float64) lipgloss.Color { return grad[int(math.Round(clamp(t, 0, 1)*100))] }

var hBlocks = []rune(" ▏▎▍▌▋▊▉█")
var vBlocks = []rune(" ▁▂▃▄▅▆▇█")

// bar draws a thin line meter. It fills in half-cell steps, colors each cell by position, and draws the empty part as a dim line.
func bar(pct float64, w int) string {
	if w <= 0 {
		return ""
	}
	halves := int(math.Round(clamp(pct, 0, 100) / 100 * float64(w*2)))
	full, half := halves/2, halves%2
	var b strings.Builder
	for i := 0; i < full; i++ {
		b.WriteString(paint(pen{fg: string(gradAt(float64(i) / float64(max(w-1, 1))))}, "━"))
	}
	if half == 1 {
		b.WriteString(paint(pen{fg: string(gradAt(float64(full) / float64(max(w-1, 1))))}, "╸"))
	}
	if rest := w - full - half; rest > 0 {
		b.WriteString(paint(fgPen(cTrack), strings.Repeat("━", rest)))
	}
	return b.String()
}

type segment struct {
	value uint64
	color lipgloss.TerminalColor
}

// segBar draws a thin line made of several colored segments. The rest is a dim line.
func segBar(total uint64, segs []segment, w int) string {
	if w <= 0 || total == 0 {
		return ""
	}
	var b strings.Builder
	var acc uint64
	drawn := 0
	for _, s := range segs {
		acc += s.value
		end := min(w, int(math.Round(float64(acc)/float64(total)*float64(w))))
		if n := end - drawn; n > 0 {
			b.WriteString(paint(fgPen(s.color), strings.Repeat("━", n)))
			drawn = end
		}
	}
	if drawn < w {
		b.WriteString(paint(fgPen(cTrack), strings.Repeat("━", w-drawn)))
	}
	return b.String()
}

// graph draws a history of 0-100 values as an h-row bar chart with a scale on the left.
func graph(vals []float64, w, h int) []string {
	const axisW = 5
	gw := w - axisW
	if gw <= 0 || h <= 0 {
		return nil
	}
	if len(vals) > gw {
		vals = vals[len(vals)-gw:]
	}
	pad := gw - len(vals)

	lines := make([]string, h)
	for r := 0; r < h; r++ {
		level := h - 1 - r // 0 = bottom row
		var row strings.Builder
		row.WriteString(strings.Repeat(" ", pad))
		for _, v := range vals {
			u := int(math.Round(clamp(v, 0, 100)/100*float64(h*8))) - level*8
			switch {
			case u >= 8:
				row.WriteRune('█')
			case u > 0:
				row.WriteRune(vBlocks[u])
			default:
				row.WriteRune(' ')
			}
		}

		label := ""
		switch {
		case r == 0:
			label = "100%"
		case r == h-1:
			label = "0%"
		case h >= 5 && r == h/2:
			label = "50%"
		}
		axis := paint(fgPen(cMuted), fmt.Sprintf("%4s", label)) + paint(fgPen(cBorder), "┤")
		color := gradAt(float64(level) / float64(max(h-1, 1)))
		lines[r] = axis + paint(pen{fg: string(color)}, row.String())
	}
	return lines
}

// box draws a rounded box with the title in the top border. w is the total width including the border.
// It is drawn every frame, so it is built directly instead of through lipgloss. Only lines that are too wide get wrapped.
func box(title, right, body string, w int) string {
	inner := w - 2
	t := " " + title + " "
	r := ""
	if right != "" {
		r = " " + right + " "
	}
	fill := max(0, inner-1-textWidth(t)-textWidth(r)-1)
	var b strings.Builder
	b.WriteString(paint(fgPen(cBorder), "╭─") + sTitle.Render(t) + paint(fgPen(cBorder), strings.Repeat("─", fill)) +
		r + paint(fgPen(cBorder), "─╮"))
	side := paint(fgPen(cBorder), "│")
	cw := inner - 2
	put := func(l string) {
		b.WriteString("\n" + side + " " + l + strings.Repeat(" ", max(0, cw-textWidth(l))) + " " + side)
	}
	for _, l := range strings.Split(body, "\n") {
		if textWidth(l) > cw {
			for _, wl := range strings.Split(lipgloss.NewStyle().Width(cw).Render(l), "\n") {
				put(wl)
			}
			continue
		}
		put(l)
	}
	b.WriteString("\n" + paint(fgPen(cBorder), "╰"+strings.Repeat("─", inner)+"╯"))
	return b.String()
}

// textWidth counts the terminal cells a string occupies, ignoring color codes.
// It is much cheaper than lipgloss.Width (no grapheme clustering).
func textWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
					i++
				}
				i++
			}
		case c < 0x80:
			w++
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			w += runewidth.RuneWidth(r)
			i += size
		}
	}
	return w
}

func pctText(p float64) string {
	return paint(pen{fg: string(gradAt(p / 100)), bold: true}, fmt.Sprintf("%5.1f%%", p))
}

func pctShort(p float64) string {
	return paint(pen{fg: string(gradAt(p / 100))}, fmt.Sprintf("%3.0f%%", p))
}

func padRight(s string, w int) string {
	if d := w - textWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// spread places left and right text at the two ends of width w.
func spread(left, right string, w int) string {
	gap := w - textWidth(left) - textWidth(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func gb(b uint64) string {
	return fmt.Sprintf("%.1f", float64(b)/(1<<30))
}

func fmtUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// spark draws a one-row history graph, coloring each cell by its value.
func spark(vals []float64, w int) string {
	if w <= 0 {
		return ""
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", w-len(vals)))
	for _, v := range vals {
		v = clamp(v, 0, 100)
		i := int(math.Round(v / 100 * 8))
		if i == 0 && v > 0.5 {
			i = 1
		}
		if i == 0 {
			b.WriteString(paint(fgPen(cBorder), "▁"))
			continue
		}
		b.WriteString(paint(pen{fg: string(gradAt(v / 100))}, string(vBlocks[i])))
	}
	return b.String()
}

func fmtBytes(b uint64) string {
	f := float64(b)
	switch {
	case f >= 1<<30:
		return fmt.Sprintf("%.1f GB", f/(1<<30))
	case f >= 1<<20:
		return fmt.Sprintf("%.0f MB", f/(1<<20))
	case f >= 1<<10:
		return fmt.Sprintf("%.0f KB", f/(1<<10))
	}
	return fmt.Sprintf("%d B", b)
}

func fmtMHz(mhz float64) string {
	if mhz <= 0 {
		return "idle"
	}
	return fmt.Sprintf("%.2f GHz", mhz/1000)
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func fmtPower(mw float64) string {
	if mw >= 1000 {
		return fmt.Sprintf("%.2f W", mw/1000)
	}
	return fmt.Sprintf("%.0f mW", mw)
}

func padLeft(s string, w int) string {
	if d := w - textWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}
