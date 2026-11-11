package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

const histLen = 600

var intervals = []time.Duration{
	250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second,
}

type tab int

const (
	tabOverview tab = iota
	tabCores
	tabProcs
)

var tabNames = []string{"Overview", "Cores", "Processes"}

type sortKey int

const (
	sortCPU sortKey = iota
	sortP
	sortE
	sortMem
	sortPower
	sortPID
	sortName
	sortCount
)

var sortNames = []string{"CPU", "P-core", "E-core", "memory", "power", "PID", "name"}

type tickMsg time.Time

// layoutState shares layout facts computed in View (such as scroll limits) with Update.
type layoutState struct {
	maxScroll int
	procOff   int
	procRows  int
	topRows   int

	// Rendered parts that change only once per interval (process table, ...). Cleared when ver changes.
	cacheVer int
	cache    map[string]string
}

type model struct {
	info  CPUInfo
	cpuS  cpuSampler
	procS *procSampler

	cores []Usage // for numbers (averaged over the refresh interval)
	total Usage
	freqs []CoreFreq

	// for bars and graphs (anim.go)
	fastS     cpuSampler
	fTotal    Usage // target values
	fCores    []Usage
	dTotal    Usage // values currently drawn
	dCores    []Usage
	dMem      [5]float64
	emaTotal  float64
	emaCores  []float64
	procEMA   map[int][3]float64
	smooth    bool
	animating bool
	lastFrame time.Time

	coreHist [][]float64
	cpuHist  []float64
	mem      MemInfo
	sys      SysInfo
	procs    []ProcStat
	hidden   int

	tab        tab
	sortBy     sortKey
	sel        int
	selPID     int
	scroll     int
	ivIdx      int
	paused     bool
	perMachine bool
	ready      bool
	err        error
	now        time.Time

	width, height int
	lay           *layoutState
	ver           int // bumped whenever the process list, selection, or size changes
}

func newModel() model {
	m := model{info: loadCPUInfo(), procS: newProcSampler(), ivIdx: 2, lay: &layoutState{}, smooth: true, procEMA: map[int][3]float64{}}
	m.coreHist = make([][]float64, m.info.Logical)
	m.cpuS.sample() // first baseline
	m.fastS.sample()
	m.procS.sample()
	return m
}

func (m model) interval() time.Duration { return intervals[m.ivIdx] }

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tick(400*time.Millisecond), cpuTick(m.cpuPeriod()))
}

func push(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > histLen {
		h = h[len(h)-histLen:]
	}
	return h
}

func (m *model) refresh() {
	m.now = time.Now()
	cores, total, err := m.cpuS.sample()
	if err != nil {
		m.err = err
		return
	}
	if len(cores) > 0 {
		m.cores, m.total = cores, total
		m.ready = true
	}
	if mi, err := sampleMem(); err == nil {
		first := m.mem.Total == 0
		m.mem = mi
		if first || !m.smooth {
			m.dMem = memVec(mi)
		}
	}
	m.sys = sampleSys()
	m.refreshTabData()
}

// refreshTabData collects only what the current screen needs.
// Frequencies (IOReport) and the process list are most of the cost, so they are skipped when not on screen.
func (m *model) refreshTabData() {
	if m.tab != tabProcs {
		if f := sampleFreq(); len(f) == m.info.Logical {
			m.freqs = f
		} else {
			m.freqs = nil
		}
	}
	if m.tab != tabCores {
		m.procs, m.hidden = m.procS.sample()
		m.smoothProcs()
		m.sortProcs()
		m.ver++
	}
}

// fillThreads reads thread counts only for rows that will be visible.
func (m *model) fillThreads() {
	lo, hi := 0, max(m.lay.topRows, 20) // row count is unknown before the first render
	if m.tab == tabProcs {
		rows := max(m.lay.procRows, 10)
		lo, hi = m.sel-rows, m.sel+rows
	}
	for k := max(0, lo); k < min(hi, len(m.procs)); k++ {
		if m.procs[k].Threads < 0 {
			m.procs[k].Threads = m.procS.threadCount(m.procs[k].PID)
		}
	}
}

func (m *model) sortProcs() {
	key := func(p ProcStat) float64 {
		switch m.sortBy {
		case sortP:
			return p.SortP
		case sortE:
			return p.SortE
		case sortMem:
			return float64(p.Mem)
		case sortPower:
			return p.PowerMW
		}
		return p.SortCPU
	}
	sort.SliceStable(m.procs, func(i, j int) bool {
		a, b := m.procs[i], m.procs[j]
		switch m.sortBy {
		case sortPID:
			return a.PID < b.PID
		case sortName:
			if x, y := strings.ToLower(a.Name), strings.ToLower(b.Name); x != y {
				return x < y
			}
			return a.PID < b.PID
		}
		if x, y := key(a), key(b); x != y {
			return x > y
		}
		if a.CPUTime != b.CPUTime {
			return a.CPUTime > b.CPUTime
		}
		return a.PID < b.PID
	})
	m.sel = 0
	for i, p := range m.procs {
		if p.PID == m.selPID {
			m.sel = i
			break
		}
	}
	if len(m.procs) > 0 {
		m.selPID = m.procs[m.sel].PID
	}
	m.fillThreads()
}

func (m *model) move(d int) {
	if m.tab == tabProcs {
		if len(m.procs) == 0 {
			return
		}
		m.sel = max(0, min(len(m.procs)-1, m.sel+d))
		m.selPID = m.procs[m.sel].PID
		m.fillThreads()
		return
	}
	m.scroll = max(0, min(m.lay.maxScroll, m.scroll+d))
}

func (m *model) setTab(t tab) {
	if m.tab == t {
		return
	}
	m.tab = t
	m.scroll = 0
	if m.ready {
		m.refreshTabData()
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ver++
	case tickMsg:
		if !m.paused {
			m.refresh()
		}
		return m, tea.Batch(tick(m.interval()), m.startAnim())
	case cpuTickMsg:
		if !m.paused {
			m.sampleFast()
		}
		return m, tea.Batch(cpuTick(m.cpuPeriod()), m.startAnim())
	case frameMsg:
		now := time.Time(msg)
		dt := now.Sub(m.lastFrame).Seconds()
		m.lastFrame = now
		if m.smooth && m.step(dt) {
			return m, frame()
		}
		m.animating = false
		return m, nil
	case tea.KeyMsg:
		m.ver++
		page := max(1, m.lay.procRows-1)
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "tab", "right", "l":
			m.setTab((m.tab + 1) % 3)
		case "shift+tab", "left", "h":
			m.setTab((m.tab + 2) % 3)
		case "1":
			m.setTab(tabOverview)
		case "2":
			m.setTab(tabCores)
		case "3":
			m.setTab(tabProcs)
		case "p", " ":
			m.paused = !m.paused
		case "c":
			m.perMachine = !m.perMachine
		case "a":
			m.smooth = !m.smooth
			if !m.smooth {
				m.settle()
			}
			return m, m.startAnim()
		case "+", "=":
			m.ivIdx = min(len(intervals)-1, m.ivIdx+1)
		case "-", "_":
			m.ivIdx = max(0, m.ivIdx-1)
		case "s":
			m.sortBy = (m.sortBy + 1) % sortCount
			m.sortProcs()
		case "S":
			m.sortBy = (m.sortBy + sortCount - 1) % sortCount
			m.sortProcs()
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "pgup", "ctrl+u":
			m.move(-page)
		case "pgdown", "ctrl+d":
			m.move(page)
		case "home", "g":
			m.move(-1 << 30)
		case "end", "G":
			m.move(1 << 30)
		}
		return m, m.startAnim()
	}
	return m, nil
}

func main() {
	snap := flag.String("snapshot", "", "render each screen once and exit, e.g. -snapshot 120x45")
	flag.Parse()
	if *snap != "" {
		snapshot(*snap)
		return
	}
	if f := os.Getenv("MACMON_CPUPROFILE"); f != "" {
		out, err := os.Create(f)
		if err == nil {
			pprof.StartCPUProfile(out)
			defer pprof.StopCPUProfile()
		}
	}
	// Each frame allocates short-lived memory, so run GC and return memory to the OS less often.
	debug.SetGCPercent(400)
	runewidth.DefaultCondition.EastAsianWidth = false
	p := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithFPS(20))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		pprof.StopCPUProfile()
		os.Exit(1)
	}
}

// snapshot renders all three screens to stdout.
func snapshot(size string) {
	var w, h int
	if _, err := fmt.Sscanf(size, "%dx%d", &w, &h); err != nil {
		fmt.Fprintln(os.Stderr, "size must look like 120x45")
		os.Exit(2)
	}
	if os.Getenv("CLICOLOR_FORCE") != "" {
		lipgloss.SetColorProfile(termenv.TrueColor) // for checking colors
	}
	m := newModel()
	m.width, m.height = w, h
	for i := 0; i < 2; i++ {
		time.Sleep(time.Second)
		m.refresh()
		m.sampleFast()
	}
	m.settle()
	for t := tabOverview; t <= tabProcs; t++ {
		m.tab = t
		fmt.Println(m.View())
		fmt.Println(strings.Repeat("=", w))
	}
}
