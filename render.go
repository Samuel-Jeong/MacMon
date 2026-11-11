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

// ---- 빠른 색칠 ----
// lipgloss.Style.Render 는 칸 하나에 쓰기엔 무겁다. 막대 · 그래프처럼 칸이 많은 곳은
// 색 조합별 이스케이프 코드를 한 번만 만들어 두고 재사용한다.

type pen struct {
	fg, bg string // "#RRGGBB" 또는 ""
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

// 0% → 100% 색 띠를 미리 101칸 만들어 둔다.
var grad [101]lipgloss.Color

func init() {
	hexes := []string{"#89DDFF", "#C3E88D", "#FFCB6B", "#F07178"} // 하늘 → 연두 → 호박 → 산호
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

// bar 는 가는 선 막대다. 반 칸 단위로 채우고, 칸 위치에 따라 색이 바뀐다. 빈 곳은 흐린 선이다.
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

// segBar 는 여러 구간을 이어 붙인 가는 선 막대다. 남는 칸은 흐린 선으로 둔다.
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

// graph 는 0~100 값의 이력을 h줄 높이의 막대 그래프로 그린다. 왼쪽에 눈금이 붙는다.
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
		level := h - 1 - r // 0 = 맨 아래 줄
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

// box 는 제목이 위 테두리에 박힌 둥근 상자다. w 는 테두리를 포함한 전체 폭이다.
// 프레임마다 그리므로 lipgloss 를 거치지 않고 직접 만든다. 폭을 넘는 줄만 줄바꿈한다.
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

// textWidth 는 색 코드를 빼고 화면에 차지하는 칸 수를 센다.
// lipgloss.Width 보다 훨씬 가볍다(글자 묶음 분석을 하지 않는다).
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

// spread 는 왼쪽·오른쪽 글을 폭 w 의 양 끝에 붙인다.
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
		return fmt.Sprintf("%d일 %d시간", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d시간 %d분", hours, mins)
	default:
		return fmt.Sprintf("%d분", mins)
	}
}

// spark 는 한 줄짜리 이력 그래프다. 칸마다 값에 맞는 색을 쓴다.
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
		return "유휴"
	}
	return fmt.Sprintf("%.2f GHz", mhz/1000)
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d시간 %d분", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d분 %d초", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%.1f초", d.Seconds())
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
