package main

import (
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Smooth animation
//
// Numbers and bars are handled separately.
//   - Numbers: averaged over the refresh interval (1 s by default), so they change once per interval and stay readable.
//   - Bars and graphs: CPU is sampled separately every quarter interval (nearly free),
//     and bars glide toward each new value at 20 frames per second.
// Process order uses a few seconds' average so rows do not reshuffle every second.

const (
	frameDur = 50 * time.Millisecond
	easeTau  = 0.2 // seconds; the gap to the target shrinks to about 1/3 every tau
	histEMA  = 0.5 // smoothing factor for graph values
	procEMA  = 0.4 // weight of the new sample in the process sort average
)

type cpuTickMsg time.Time
type frameMsg time.Time

func cpuTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return cpuTickMsg(t) })
}

func frame() tea.Cmd {
	return tea.Tick(frameDur, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// cpuPeriod is the CPU sampling interval used for bars.
func (m model) cpuPeriod() time.Duration {
	d := m.interval() / 4
	return max(100*time.Millisecond, min(500*time.Millisecond, d))
}

// sampleFast takes a new CPU sample for bars and graphs.
func (m *model) sampleFast() {
	cores, total, err := m.fastS.sample()
	if err != nil || len(cores) == 0 {
		return
	}
	m.fTotal, m.fCores = total, cores
	if len(m.dCores) != len(cores) {
		m.dCores = append([]Usage(nil), cores...)
		m.dTotal = total
		m.emaCores = make([]float64, len(cores))
		for i := range cores {
			m.emaCores[i] = cores[i].Total
		}
		m.emaTotal = total.Total
	}
	m.emaTotal += (total.Total - m.emaTotal) * histEMA
	m.cpuHist = push(m.cpuHist, m.emaTotal)
	for i := range cores {
		m.emaCores[i] += (cores[i].Total - m.emaCores[i]) * histEMA
		if i < len(m.coreHist) {
			m.coreHist[i] = push(m.coreHist[i], m.emaCores[i])
		}
	}
	// The process screen has no moving bars, so skip the frame loop there.
	if !m.smooth || m.tab == tabProcs {
		m.settle()
	}
}

// settle snaps bars to their target values.
func (m *model) settle() {
	m.dTotal = m.fTotal
	m.dCores = append(m.dCores[:0], m.fCores...)
	m.dMem = memVec(m.mem)
}

// startAnim restarts the frame loop if it is idle.
func (m *model) startAnim() tea.Cmd {
	if !m.smooth || m.animating || m.tab == tabProcs {
		return nil
	}
	m.animating = true
	m.lastFrame = time.Now()
	return frame()
}

// step moves bar values toward their targets by dt and reports whether anything is still moving.
func (m *model) step(dt float64) bool {
	a := 1 - math.Exp(-dt/easeTau)
	moving := false
	ease := func(cur *float64, tgt, eps float64) {
		d := tgt - *cur
		if math.Abs(d) <= eps {
			*cur = tgt
			return
		}
		*cur += d * a
		moving = true
	}
	easeU := func(cur *Usage, tgt Usage) {
		ease(&cur.Total, tgt.Total, 0.05)
		ease(&cur.User, tgt.User, 0.05)
		ease(&cur.Sys, tgt.Sys, 0.05)
	}
	easeU(&m.dTotal, m.fTotal)
	for i := range m.dCores {
		if i < len(m.fCores) {
			easeU(&m.dCores[i], m.fCores[i])
		}
	}
	tgt := memVec(m.mem)
	for i := range m.dMem {
		ease(&m.dMem[i], tgt[i], 1<<20) // stop once within 1 MB
	}
	return moving
}

// memVec holds the values drawn in the memory bars (app, wired, compressed, cached, swap).
func memVec(mi MemInfo) [5]float64 {
	return [5]float64{float64(mi.App), float64(mi.Wired), float64(mi.Compressed), float64(mi.Cached), float64(mi.SwapUsed)}
}

// smoothProcs updates the averages used for sorting. Displayed numbers are left as is.
func (m *model) smoothProcs() {
	next := make(map[int][3]float64, len(m.procs))
	for i := range m.procs {
		p := &m.procs[i]
		cur := [3]float64{p.CPU, p.PCPU, p.ECPU}
		if e, ok := m.procEMA[p.PID]; ok {
			for j := range cur {
				cur[j] = e[j] + (cur[j]-e[j])*procEMA
			}
		}
		next[p.PID] = cur
		p.SortCPU, p.SortP, p.SortE = cur[0], cur[1], cur[2]
	}
	m.procEMA = next
}
