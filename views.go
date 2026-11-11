package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	cUser  = lipgloss.Color("#82AAFF")
	cSys   = lipgloss.Color("#F07178")
	sKey   = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sTabOn = lipgloss.NewStyle().Foreground(lipgloss.Color("#1E1B3A")).Background(cAccent).Bold(true).Padding(0, 1)
	sTabOf = lipgloss.NewStyle().Foreground(cMuted).Padding(0, 1)

	// default, maintenance, background, utility, legacy, user-initiated, user-interactive
	qosColors = [7]lipgloss.Color{"#9A9BB8", "#6C7394", "#C792EA", "#89DDFF", "#55597A", "#FFCB6B", "#F07178"}
)

func fg(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

func coreColor(isE bool) lipgloss.Color {
	if isE {
		return cECore
	}
	return cPCore
}

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	W := m.width
	if !m.ready {
		return "\n  " + sMuted.Render("measuring…")
	}
	if m.lay.cacheVer != m.ver || m.lay.cache == nil {
		m.lay.cache = map[string]string{}
		m.lay.cacheVer = m.ver
	}
	header := m.viewHeader(W)
	footer := m.viewFooter(W)
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 2

	var body string
	switch m.tab {
	case tabOverview:
		body = m.scrolled(m.viewOverview(W, bodyH), bodyH)
	case tabCores:
		body = m.scrolled(m.viewCores(W), bodyH)
	case tabProcs:
		m.lay.maxScroll = 0
		body = m.cached(fmt.Sprintf("procs|%d|%d", W, bodyH), func() string { return m.viewProcs(W, bodyH) })
	}

	lines := strings.Split(header+"\n\n"+body, "\n")
	for len(lines) < m.height-lipgloss.Height(footer) {
		lines = append(lines, "")
	}
	page := strings.Join(lines, "\n") + "\n" + footer
	return page
}

// cached renders a part only once per ver.
func (m model) cached(key string, draw func() string) string {
	if v, ok := m.lay.cache[key]; ok {
		return v
	}
	v := draw()
	m.lay.cache[key] = v
	return v
}

func (m model) scrolled(content string, h int) string {
	lines := strings.Split(content, "\n")
	maxS := max(0, len(lines)-h)
	m.lay.maxScroll = maxS
	s := min(m.scroll, maxS)
	return strings.Join(lines[s:min(s+max(h, 0), len(lines))], "\n")
}

// ---- Header and footer ----

func (m model) viewHeader(W int) string {
	spec := fmt.Sprintf("  ·  %d performance + %d efficiency cores", m.info.P.Count, m.info.E.Count)
	if m.info.E.Count == 0 {
		spec = fmt.Sprintf("  ·  %d cores", m.info.Logical)
	}
	clock := sMuted.Render(m.now.Format("15:04:05"))
	left := sTitle.Render("◆ MacMon") + "  " + sBold.Render(m.info.Model) + sMuted.Render(spec)
	if textWidth(left)+textWidth(clock)+1 > W && m.info.E.Count > 0 {
		left = sTitle.Render("◆ MacMon") + "  " + sBold.Render(m.info.Model) +
			sMuted.Render(fmt.Sprintf("  ·  %dP + %dE", m.info.P.Count, m.info.E.Count))
	}
	line1 := spread(left, clock, W)

	var tabs []string
	for i, n := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, n)
		if tab(i) == m.tab {
			tabs = append(tabs, sTabOn.Render(label))
		} else {
			tabs = append(tabs, sTabOf.Render(label))
		}
	}
	l := m.sys.Load
	tabLine := strings.Join(tabs, " ")
	status := sMuted.Render(fmt.Sprintf("up %s  ·  load %.2f %.2f %.2f", fmtUptime(m.sys.Uptime), l[0], l[1], l[2]))
	if textWidth(tabLine)+textWidth(status)+1 > W {
		status = sMuted.Render(fmt.Sprintf("load %.2f", l[0]))
	}
	return line1 + "\n" + spread(tabLine, status, W)
}

func (m model) viewFooter(W int) string {
	k := func(key, desc string) string { return sKey.Render(key) + " " + sMuted.Render(desc) }
	parts := []string{k("tab", "screen")}
	switch {
	case m.tab == tabProcs:
		parts = append(parts, k("↑↓", "select"), k("s", "sort: "+sortNames[m.sortBy]), k("c", "unit"))
	case m.lay.maxScroll > 0:
		parts = append(parts, k("↑↓", "scroll"))
	}
	smooth := "off"
	if m.smooth {
		smooth = "on"
	}
	parts = append(parts, k("a", "smooth "+smooth), k("p", "pause"), k("+/-", fmt.Sprintf("interval %.2gs", m.interval().Seconds())), k("q", "quit"))
	right := ""
	switch {
	case m.err != nil:
		right = fg(cBad).Render("error: " + m.err.Error())
	case m.paused:
		right = fg(cWarn).Bold(true).Render("PAUSED")
	}
	// If too wide, tighten spacing, then drop items from the second-to-last backward ("q quit" stays).
	fits := func(sep string) bool { return textWidth(strings.Join(parts, sep))+textWidth(right)+1 <= W }
	sep := "   "
	if !fits(sep) {
		sep = "  "
	}
	for !fits(sep) && len(parts) > 2 {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1])
	}
	return spread(strings.Join(parts, sep), right, W)
}

// ---- Overview ----

func (m model) viewOverview(W, H int) string {
	cw := W - 4
	coreLines := m.coreGrid(cw)
	memLines := m.memLines(cw)
	fixed := 2 + 1 + 1 + len(coreLines) + 2 + len(memLines)
	spare := H - fixed

	g, rows := 0, 0
	// Spare height goes to the graph first (up to 8 rows), and the rest to the process list.
	if spare >= 4 {
		g = min(8, spare-1)
		spare -= g + 1
	}
	if spare >= 4 {
		rows = spare - 3
		spare = 0
	}
	if g > 0 && spare > 0 {
		g += spare
	}

	var cpu []string
	cpu = append(cpu, m.totalLine(cw))
	if g >= 3 {
		cpu = append(cpu, graph(m.cpuHist, cw, g)...)
	}
	cpu = append(cpu, "")
	cpu = append(cpu, coreLines...)

	out := box("CPU", sMuted.Render(fmt.Sprintf("%d logical cores", m.info.Logical)), strings.Join(cpu, "\n"), W) + "\n" +
		box("Memory", sMuted.Render(fmtBytes(m.mem.Total)), strings.Join(memLines, "\n"), W)
	if rows > 0 {
		out += "\n" + m.cached(fmt.Sprintf("top|%d|%d", W, rows), func() string { return m.procBox(W, rows, false) })
	}
	return out
}

func (m model) totalLine(cw int) string {
	lbl := sBold.Render("Total")
	right := pctText(m.total.Total) +
		sMuted.Render("   user ") + fg(cUser).Render(fmt.Sprintf("%4.1f%%", m.total.User)) +
		sMuted.Render("   system ") + fg(cSys).Render(fmt.Sprintf("%4.1f%%", m.total.Sys))
	bw := cw - textWidth(lbl) - textWidth(right) - 2
	return lbl + " " + bar(m.dTotal.Total, bw) + " " + right
}

// group lists the logical CPU numbers of one kind of core.
func (m model) group(isE bool) []int {
	var idx []int
	for i := 0; i < m.info.Logical; i++ {
		if m.info.IsE(i) == isE {
			idx = append(idx, i)
		}
	}
	return idx
}

func (m model) avg(idx []int) (pct, mhz float64) {
	var n int
	for _, i := range idx {
		if i < len(m.cores) {
			pct += m.cores[i].Total
		}
		if i < len(m.freqs) && m.freqs[i].MHz > 0 {
			mhz += m.freqs[i].MHz
			n++
		}
	}
	if len(idx) > 0 {
		pct /= float64(len(idx))
	}
	if n > 0 {
		mhz /= float64(n)
	}
	return
}

func (m model) coreGrid(cw int) []string {
	const gap = 3
	withFreq := m.freqs != nil
	extra := 9
	if withFreq {
		extra += 6
	}
	cols := max(1, min(6, (cw+gap)/(extra+20+gap)))
	cellW := (cw - (cols-1)*gap) / cols
	bw := cellW - extra

	var out []string
	for _, isE := range []bool{false, true} {
		idx := m.group(isE)
		if len(idx) == 0 {
			continue
		}
		name := "Performance cores"
		if isE {
			name = "Efficiency cores"
		}
		if m.info.E.Count == 0 {
			name = "Cores"
		}
		pct, mhz := m.avg(idx)
		right := sMuted.Render("avg ") + pctShort(pct)
		if withFreq {
			right += sMuted.Render("  ·  ") + sText.Render(fmtMHz(mhz))
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, spread(fg(coreColor(isE)).Bold(true).Render(name)+sMuted.Render(fmt.Sprintf("  ×%d", len(idx))), right, cw))

		rows := (len(idx) + cols - 1) / cols
		for r := 0; r < rows; r++ {
			var cells []string
			for c := 0; c < cols; c++ {
				k := c*rows + r
				if k >= len(idx) {
					break
				}
				i := idx[k]
				label, _ := m.info.CoreLabel(i)
				u, du := 0.0, 0.0
				if i < len(m.cores) {
					u = m.cores[i].Total
				}
				if i < len(m.dCores) {
					du = m.dCores[i].Total
				}
				cell := paint(fgPen(coreColor(isE)), fmt.Sprintf("%-3s", label)) + " " + bar(du, bw) + " " + pctShort(u)
				if withFreq {
					f := m.freqs[i].MHz
					fs := " idle"
					if f > 0 {
						fs = fmt.Sprintf("%4.1fG", f/1000)
					}
					cell += " " + paint(fgPen(cMuted), fs)
				}
				cells = append(cells, padRight(cell, cellW))
			}
			out = append(out, strings.Join(cells, strings.Repeat(" ", gap)))
		}
	}
	return out
}

func (m model) memLines(cw int) []string {
	mi := m.mem
	const lw = 7
	right := sBold.Render(gb(mi.Used)) + sMuted.Render(" / "+gb(mi.Total)+" GB  ") + pctShort(mi.UsedPct())
	bw := cw - lw - textWidth(right) - 2
	d := m.dMem
	line1 := padRight(sBold.Render("Used"), lw) + segBar(mi.Total, []segment{
		{uint64(d[0]), cApp}, {uint64(d[1]), cWired}, {uint64(d[2]), cCompressed}, {uint64(d[3]), cCached},
	}, bw) + "  " + right

	item := func(c lipgloss.TerminalColor, name string, v uint64) string {
		return fg(c).Render("■") + " " + sMuted.Render(name) + " " + sText.Render(gb(v)+"G")
	}
	legend := padRight("", lw) + strings.Join([]string{
		item(cApp, "app", mi.App), item(cWired, "wired", mi.Wired), item(cCompressed, "compressed", mi.Compressed),
		item(cCached, "cached", mi.Cached), item(cTrack, "free", mi.Free),
	}, "   ")

	pName, pCol := "normal", cOK
	switch mi.Pressure {
	case 2:
		pName, pCol = "warning", cWarn
	case 4:
		pName, pCol = "critical", cBad
	}
	sp := 0.0
	if mi.SwapTotal > 0 {
		sp = d[4] / float64(mi.SwapTotal) * 100
	}
	right3 := sText.Render(gb(mi.SwapUsed)) + sMuted.Render(" / "+gb(mi.SwapTotal)+" GB") +
		sMuted.Render("   memory pressure ") + fg(pCol).Bold(true).Render("● "+pName)
	bw3 := cw - lw - textWidth(right3) - 2
	line3 := padRight(sBold.Render("Swap"), lw) + bar(sp, bw3) + "  " + right3
	return []string{line1, legend, line3}
}

// ---- Cores ----

func (m model) viewCores(W int) string {
	cw := W - 4
	kw := 11
	kv := func(k, v string) string { return padRight(sMuted.Render(k), kw) + v }
	lvl := func(l Level) string {
		s := fmt.Sprintf("L1i %s · L1d %s · L2 %s (shared per cluster)", fmtBytes(l.L1i), fmtBytes(l.L1d), fmtBytes(l.L2))
		if l.FMax > 0 {
			s += fmt.Sprintf(" · %.2f–%.2f GHz, %d steps", l.FMin/1000, l.FMax/1000, l.FSteps)
		}
		return sText.Render(s)
	}
	info := []string{kv("Chip", sBold.Render(m.info.Model))}
	if m.info.E.Count > 0 {
		info = append(info,
			kv("Layout", sText.Render(fmt.Sprintf("%d performance cores (%s) · %d efficiency cores (%s)",
				m.info.P.Count, clusterText(m.info.P), m.info.E.Count, clusterText(m.info.E)))),
			kv("P-cores", lvl(m.info.P)),
			kv("E-cores", lvl(m.info.E)),
		)
	}
	if !freqOK {
		info = append(info, kv("Frequency", sMuted.Render("unavailable (IOReport not accessible)")))
	}
	info = append(info, kv("Bar colors", fg(cUser).Render("■")+sMuted.Render(" user   ")+fg(cSys).Render("■")+sMuted.Render(" system")))
	out := []string{box("Chip", "", strings.Join(info, "\n"), W)}

	for _, isE := range []bool{false, true} {
		idx := m.group(isE)
		if len(idx) == 0 {
			continue
		}
		clusters := map[int][]int{}
		var order []int
		for _, i := range idx {
			_, c := m.info.CoreLabel(i)
			if _, ok := clusters[c]; !ok {
				order = append(order, c)
			}
			clusters[c] = append(clusters[c], i)
		}
		for _, c := range order {
			members := clusters[c]
			title := fmt.Sprintf("P%d cluster · performance cores", c)
			if isE {
				title = "E cluster · efficiency cores"
				if len(order) > 1 {
					title = fmt.Sprintf("E%d cluster · efficiency cores", c)
				}
			}
			if m.info.E.Count == 0 {
				title = "Cores"
			}
			pct, mhz := m.avg(members)
			right := sMuted.Render("avg ") + pctShort(pct)
			if m.freqs != nil {
				right += sMuted.Render(" · ") + sText.Render(fmtMHz(mhz))
			}
			out = append(out, box(title, right, strings.Join(m.coreRows(members, cw, isE), "\n"), W))
		}
	}
	return strings.Join(out, "\n")
}

func (m model) coreRows(members []int, cw int, isE bool) []string {
	fixed := 4 + 1 + 6 + 1 + 1 + 6 + 1 + 8 + 1 + 8 + 1 + 10 + 1
	avail := cw - fixed
	bw := max(10, avail*45/100)
	sw := max(4, avail-bw)

	h := func(s string, w int) string { return padRight(sMuted.Render(s), w) }
	hr := func(s string, w int) string { return padLeft(sMuted.Render(s), w) }
	header := h("Core", 4) + " " + h("CPU", 6) + " " + h("Usage", bw) + " " + hr("Total", 6) + " " +
		hr("User", 8) + " " + hr("System", 8) + " " + hr("Freq", 10) + " " + h("Recent", sw)
	rows := []string{header}
	for _, i := range members {
		label, _ := m.info.CoreLabel(i)
		var u, du Usage
		if i < len(m.cores) {
			u = m.cores[i]
		}
		if i < len(m.dCores) {
			du = m.dCores[i]
		}
		f := "—"
		if i < len(m.freqs) {
			f = fmtMHz(m.freqs[i].MHz)
		}
		row := fg(coreColor(isE)).Bold(true).Render(fmt.Sprintf("%-4s", label)) + " " +
			sMuted.Render(fmt.Sprintf("%-6s", fmt.Sprintf("cpu%d", i))) + " " +
			segBar(1000, []segment{{uint64(du.User * 10), cUser}, {uint64(du.Sys * 10), cSys}}, bw) + " " +
			pctText(u.Total) + " " +
			fg(cUser).Render(fmt.Sprintf("%7.1f%%", u.User)) + " " +
			fg(cSys).Render(fmt.Sprintf("%7.1f%%", u.Sys)) + " " +
			sText.Render(fmt.Sprintf("%10s", f)) + " " +
			spark(m.coreHist[i], sw)
		rows = append(rows, row)
	}
	return rows
}

// ---- Processes ----

type pcol struct {
	id, title string
	w         int
	right     bool
	sort      sortKey
}

// procCols decides the table columns. Name and user columns fit their longest value,
// and leftover width goes to the P/E usage bar. When space runs out, less important columns are dropped first.
func procCols(cw int, full bool, nameNeed, userNeed int) []pcol {
	userW := max(6, userNeed)
	cols := []pcol{
		{"pid", "PID", 7, true, sortPID},
		{"name", "Name", 0, false, sortName},
		{"user", "User", userW, false, -1},
		{"cpu", "CPU%", 7, true, sortCPU},
		{"p", "P-core", 7, true, sortP},
		{"e", "E-core", 7, true, sortE},
		{"dist", "P/E usage", 10, false, -1},
		{"thr", "Threads", 7, true, -1},
		{"mem", "Memory", 9, true, sortMem},
		{"pow", "Power", 9, true, sortPower},
	}
	drop := []string{"user", "pow", "thr"}
	if !full {
		drop = []string{"thr", "mem"}
		cols = []pcol{
			{"pid", "PID", 7, true, sortPID}, {"name", "Name", 0, false, sortName},
			{"cpu", "CPU%", 7, true, sortCPU}, {"p", "P-core", 7, true, sortP},
			{"e", "E-core", 7, true, sortE}, {"dist", "P/E usage", 10, false, -1},
			{"thr", "Threads", 7, true, -1}, {"mem", "Memory", 9, true, sortMem},
		}
	}
	width := func() int {
		w := len(cols) - 1
		for _, c := range cols {
			w += c.w
		}
		return w
	}
	nameNeed = max(8, nameNeed)
	for _, d := range drop {
		if cw-width() >= nameNeed {
			break
		}
		for i, c := range cols {
			if c.id == d {
				cols = append(cols[:i], cols[i+1:]...)
				break
			}
		}
	}
	rest := cw - width()
	nameW := max(8, min(nameNeed, rest))
	distExtra := max(0, min(20, rest-nameW)) // usage bar is at most 30 cells; the rest goes to the name column
	nameW += max(0, rest-nameW-distExtra)
	for i := range cols {
		switch cols[i].id {
		case "name":
			cols[i].w = nameW
		case "dist":
			cols[i].w += distExtra
		}
	}
	return cols
}

// colNeeds returns the width needed by the name and user columns (longest value).
func (m model) colNeeds() (name, user int) {
	for _, p := range m.procs {
		name = max(name, runewidth.StringWidth(p.Name))
		user = max(user, runewidth.StringWidth(p.User))
	}
	return
}

func (m model) procCell(p ProcStat, c pcol, sel bool) string {
	pad := func(s string) string {
		// Truncate only when the screen is too narrow. Some fonts draw "…" wide, so leave one spare cell.
		if runewidth.StringWidth(s) > c.w {
			s = runewidth.Truncate(s, c.w-1, "…")
		}
		if c.right {
			return strings.Repeat(" ", max(0, c.w-runewidth.StringWidth(s))) + s
		}
		return s + strings.Repeat(" ", max(0, c.w-runewidth.StringWidth(s)))
	}
	put := func(col lipgloss.TerminalColor, s string) string {
		return paint(pen{fg: hexOf(col), bold: sel}, pad(s))
	}
	scale := m.cpuScale()
	num := func(v float64, col lipgloss.TerminalColor) string {
		if v < 0.05 {
			return put(cMuted, "0.0")
		}
		return put(col, fmt.Sprintf("%.1f", v*scale))
	}
	switch c.id {
	case "pid":
		return put(cMuted, fmt.Sprint(p.PID))
	case "name":
		if sel {
			return put(cAccent, p.Name)
		}
		return put(cText, p.Name)
	case "user":
		return put(cMuted, p.User)
	case "cpu":
		return num(p.CPU, gradAt(p.CPU/100))
	case "p":
		return num(p.PCPU, cPCore)
	case "e":
		return num(p.ECPU, cECore)
	case "dist":
		return distBar(p, c.w)
	case "thr":
		if p.Threads < 0 {
			return put(cMuted, "")
		}
		return put(cMuted, fmt.Sprint(p.Threads))
	case "mem":
		return put(cText, fmtBytes(p.Mem))
	case "pow":
		if p.PowerMW < 0.5 {
			return put(cMuted, "—")
		}
		return put(cWarn, fmtPower(p.PowerMW))
	}
	return ""
}

// cpuScale converts the CPU% unit: one core = 100% by default, whole Mac = 100% after pressing c.
func (m model) cpuScale() float64 {
	if m.perMachine && m.info.Logical > 0 {
		return 1 / float64(m.info.Logical)
	}
	return 1
}

func (m model) unitName() string {
	if m.perMachine {
		return "whole Mac = 100%"
	}
	return "one core = 100%"
}

// procBox draws the process table. With full set, it handles selection and scrolling.
func (m model) procBox(W, rows int, full bool) string {
	cw := W - 4
	nameNeed, userNeed := m.colNeeds()
	cols := procCols(cw-2, full, nameNeed, userNeed) // the left 2 cells hold the selection marker

	var hdr []string
	for _, c := range cols {
		t := c.title
		if full && c.sort == m.sortBy {
			t += "▾"
		}
		w := c.w - runewidth.StringWidth(t)
		st := sMuted
		if full && c.sort == m.sortBy {
			st = sKey
		}
		if c.right {
			hdr = append(hdr, strings.Repeat(" ", max(0, w))+st.Render(t))
		} else {
			hdr = append(hdr, st.Render(t)+strings.Repeat(" ", max(0, w)))
		}
	}
	lines := []string{"  " + strings.Join(hdr, " ")}

	off := 0
	if full {
		off = m.lay.procOff
		if m.sel < off {
			off = m.sel
		}
		if m.sel >= off+rows {
			off = m.sel - rows + 1
		}
		off = max(0, min(off, len(m.procs)-rows))
		m.lay.procOff = off
		m.lay.procRows = rows
	}
	for k := off; k < min(off+rows, len(m.procs)); k++ {
		p := m.procs[k]
		sel := full && k == m.sel
		cells := make([]string, len(cols))
		for i, c := range cols {
			cells[i] = m.procCell(p, c, sel)
		}
		mark := "  "
		if sel {
			mark = paint(fgPen(cAccent), "▍") + " "
		}
		lines = append(lines, mark+strings.Join(cells, " "))
	}

	title := "Top processes"
	right := sMuted.Render("CPU% unit: " + m.unitName() + "  ·  3 → full list")
	if !full {
		m.lay.topRows = rows
	}
	if full {
		title = "Processes"
		pos := 0
		if len(m.procs) > 0 {
			pos = m.sel + 1
		}
		right = sMuted.Render(fmt.Sprintf("CPU%% unit: %s  ·  %d / %d", m.unitName(), pos, len(m.procs)))
		if m.hidden > 0 {
			for len(lines) < rows+1 {
				lines = append(lines, "")
			}
			lines = append(lines, sMuted.Render(fmt.Sprintf(
				"%d processes owned by other users (root, …) are hidden · run with sudo to see all", m.hidden)))
		}
	}
	return box(title, right, strings.Join(lines, "\n"), W)
}

func (m model) viewProcs(W, H int) string {
	detail := m.procDetail(W)
	note := 0
	if m.hidden > 0 {
		note = 1
	}
	rows := max(3, H-lipgloss.Height(detail)-3-note)
	return m.procBox(W, rows, true) + "\n" + detail
}

func (m model) procDetail(W int) string {
	cw := W - 4
	if len(m.procs) == 0 {
		return box("Selected process", "", sMuted.Render("no processes"), W)
	}
	p := m.procs[m.sel]
	const lw = 16
	lbl := func(s string) string { return padRight(sMuted.Render(s), lw) }
	sep := sMuted.Render(" · ")

	if p.Threads < 0 {
		p.Threads = m.procS.threadCount(p.PID)
	}
	line1 := sBold.Render(p.Name) + sMuted.Render(fmt.Sprintf("   PID %d · parent %d · %s · %d threads",
		p.PID, p.PPID, p.User, p.Threads))

	// P/E split for this interval
	sc := m.cpuScale()
	nowTxt := fg(cPCore).Render(fmt.Sprintf("performance %.1f%%", p.PCPU*sc)) + sep + fg(cECore).Render(fmt.Sprintf("efficiency %.1f%%", p.ECPU*sc)) +
		sMuted.Render("  ("+m.unitName()+")")
	bw := max(10, cw-lw-textWidth(nowTxt)-2)
	var nowBar string
	if p.CPU >= 0.05 {
		nowBar = segBar(1000, []segment{{uint64(p.PCPU / p.CPU * 1000), cPCore}, {uint64(p.ECPU / p.CPU * 1000), cECore}}, bw)
	} else {
		nowBar = paint(fgPen(cTrack), strings.Repeat("━", bw))
	}
	line2 := lbl("P/E · now") + nowBar + "  " + nowTxt

	lifeTxt := fg(cPCore).Render(fmt.Sprintf("performance %.0f%%", p.PShare*100)) + sep + fg(cECore).Render(fmt.Sprintf("efficiency %.0f%%", (1-p.PShare)*100))
	if p.CPUTime == 0 {
		lifeTxt = sMuted.Render("no history")
	}
	lifeTxt = padRight(lifeTxt, textWidth(nowTxt))
	line3 := lbl("P/E · lifetime") + segBar(1000, []segment{{uint64(p.PShare * 1000), cPCore}, {uint64((1 - p.PShare) * 1000), cECore}}, bw) + "  " + lifeTxt

	// QoS
	var segs []segment
	type qs struct {
		i int
		v float64
	}
	var top []qs
	for i := 0; i < 7; i++ {
		segs = append(segs, segment{uint64(p.QoS[i] * 1000), qosColors[i]})
		if p.QoS[i] >= 0.01 {
			top = append(top, qs{i, p.QoS[i]})
		}
	}
	for a := 0; a < len(top); a++ {
		for b := a + 1; b < len(top); b++ {
			if top[b].v > top[a].v {
				top[a], top[b] = top[b], top[a]
			}
		}
	}
	var qtxt []string
	for _, t := range top {
		qtxt = append(qtxt, fg(qosColors[t.i]).Render("■")+" "+sText.Render(fmt.Sprintf("%s %.0f%%", qosNames[t.i], t.v*100)))
	}
	when := "now"
	if !p.QoSLive {
		when = "lifetime"
	}
	qline := strings.Join(qtxt, "  ")
	qbw := max(10, min(bw, cw-lw-textWidth(qline)-2))
	line4 := lbl("QoS · "+when) + segBar(1000, segs, qbw) + "  " + qline

	ipc := "—"
	if p.IPC > 0 {
		ipc = fmt.Sprintf("%.2f", p.IPC)
	}
	line5 := lbl("Other") + sText.Render("CPU time "+fmtDur(p.CPUTime)) + sep +
		sText.Render(fmt.Sprintf("IPC %s", ipc)) + sep +
		sText.Render("power "+fmtPower(p.PowerMW)) + sep +
		sText.Render(fmt.Sprintf("memory %s (peak %s)", fmtBytes(p.Mem), fmtBytes(p.MemMax)))
	hint := sMuted.Render("E-cores only: background · maintenance QoS  ·  send work there with ") +
		sKey.Render("taskpolicy -b <command>")

	body := strings.Join([]string{line1, "", line2, line3, line4, line5, "", hint}, "\n")
	return box("Selected process", "", body, W)
}

// distBar shows P-core and E-core usage as length. The full column width is one core (100%).
func distBar(p ProcStat, w int) string {
	if w <= 0 {
		return ""
	}
	pc := min(p.PCPU, 100) / 100 * float64(w)
	ec := min(p.ECPU, 100) / 100 * float64(w)
	if pc+ec > float64(w) {
		k := float64(w) / (pc + ec)
		pc, ec = pc*k, ec*k
	}
	pn, en := int(math.Round(pc)), int(math.Round(ec))
	if pn+en > w {
		en = w - pn
	}
	var b strings.Builder
	switch {
	case pn+en > 0:
		b.WriteString(paint(fgPen(cPCore), strings.Repeat("━", pn)))
		b.WriteString(paint(fgPen(cECore), strings.Repeat("━", en)))
	case p.CPU >= 0.05:
		c := cPCore
		if p.ECPU > p.PCPU {
			c = cECore
		}
		b.WriteString(paint(fgPen(c), "╸"))
		pn = 1
	}
	b.WriteString(strings.Repeat(" ", w-pn-en))
	return b.String()
}

// clusterText describes the cluster layout, e.g. "2 clusters of 6".
func clusterText(l Level) string {
	n := l.Clusters()
	if n == 1 {
		return fmt.Sprintf("1 cluster of %d", l.PerL2)
	}
	return fmt.Sprintf("%d clusters of %d", n, l.PerL2)
}
