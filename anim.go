package main

import (
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// 부드러운 화면
//
// 숫자와 막대를 나눠서 다룬다.
//   - 숫자: 갱신 주기(기본 1초) 평균. 1초에 한 번만 바뀌어 읽기 편하다.
//   - 막대 · 그래프: CPU 를 주기의 1/4 마다 따로 재고(비용이 거의 없다),
//     막대는 새 값까지 초당 20프레임으로 미끄러지듯 따라간다.
// 프로세스 순서는 몇 초 평균으로 정해 순위가 덜 뒤섞이게 한다.

const (
	frameDur = 50 * time.Millisecond
	easeTau  = 0.2 // 초. 새 값과의 차이가 이 시간마다 약 1/3 로 준다.
	histEMA  = 0.5 // 그래프 값의 흔들림을 줄이는 비율
	procEMA  = 0.4 // 프로세스 정렬용 평균에 새 값을 섞는 비율
)

type cpuTickMsg time.Time
type frameMsg time.Time

func cpuTick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return cpuTickMsg(t) })
}

func frame() tea.Cmd {
	return tea.Tick(frameDur, func(t time.Time) tea.Msg { return frameMsg(t) })
}

// cpuPeriod 는 막대용 CPU 측정 간격이다.
func (m model) cpuPeriod() time.Duration {
	d := m.interval() / 4
	return max(100*time.Millisecond, min(500*time.Millisecond, d))
}

// sampleFast 는 막대 · 그래프용 CPU 값을 새로 잰다.
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
	// 프로세스 탭에는 움직이는 막대가 없으니 프레임을 돌리지 않는다.
	if !m.smooth || m.tab == tabProcs {
		m.settle()
	}
}

// settle 은 막대를 목표값으로 바로 옮긴다.
func (m *model) settle() {
	m.dTotal = m.fTotal
	m.dCores = append(m.dCores[:0], m.fCores...)
	m.dMem = memVec(m.mem)
}

// startAnim 은 프레임 루프가 멈춰 있으면 다시 돌린다.
func (m *model) startAnim() tea.Cmd {
	if !m.smooth || m.animating || m.tab == tabProcs {
		return nil
	}
	m.animating = true
	m.lastFrame = time.Now()
	return frame()
}

// step 은 막대 값을 목표값 쪽으로 dt 만큼 옮기고, 아직 움직이는 중인지 돌려준다.
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
		ease(&m.dMem[i], tgt[i], 1<<20) // 1 MB 안이면 멈춘다
	}
	return moving
}

// memVec 은 메모리 막대에 쓰는 값(앱, 고정, 압축, 캐시, 스왑)이다.
func memVec(mi MemInfo) [5]float64 {
	return [5]float64{float64(mi.App), float64(mi.Wired), float64(mi.Compressed), float64(mi.Cached), float64(mi.SwapUsed)}
}

// smoothProcs 는 정렬용 평균을 갱신한다. 숫자 자체는 그대로 둔다.
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
