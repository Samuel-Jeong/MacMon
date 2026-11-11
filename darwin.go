package main

/*
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit -lIOReport
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <libproc.h>
#include <sys/resource.h>
#include <mach/mach_time.h>
#include <stdlib.h>
#include <string.h>

// ---- IOReport (비공개 API, 활성 상태 보기 · powermetrics 가 쓰는 것과 같다) ----
typedef struct __IOReportSubscriptionCF *IOReportSubscriptionRef;
CFDictionaryRef IOReportCopyChannelsInGroup(CFStringRef, CFStringRef, uint64_t, uint64_t, uint64_t);
IOReportSubscriptionRef IOReportCreateSubscription(void*, CFMutableDictionaryRef, CFMutableDictionaryRef*, uint64_t, CFTypeRef);
CFDictionaryRef IOReportCreateSamples(IOReportSubscriptionRef, CFMutableDictionaryRef, CFTypeRef);
CFDictionaryRef IOReportCreateSamplesDelta(CFDictionaryRef, CFDictionaryRef, CFTypeRef);
CFStringRef IOReportChannelGetChannelName(CFDictionaryRef);
int32_t IOReportStateGetCount(CFDictionaryRef);
CFStringRef IOReportStateGetNameForIndex(CFDictionaryRef, int32_t);
int64_t IOReportStateGetResidency(CFDictionaryRef, int32_t);

#define MAXF 64

static IOReportSubscriptionRef g_sub;
static CFMutableDictionaryRef g_subch;
static CFDictionaryRef g_prev;
static double g_ftab[2][MAXF]; // [0]=E, [1]=P, MHz
static int g_fn[2];

// pmgr 의 voltage-states 표에서 클러스터별 주파수 단계를 읽는다.
static void load_ftab(void) {
	io_iterator_t it;
	if (IOServiceGetMatchingServices(kIOMainPortDefault, IOServiceMatching("AppleARMIODevice"), &it) != KERN_SUCCESS) return;
	io_object_t e;
	while ((e = IOIteratorNext(it))) {
		io_name_t n;
		IORegistryEntryGetName(e, n);
		if (strcmp(n, "pmgr") == 0) {
			const char* keys[2] = {"voltage-states1-sram", "voltage-states5-sram"};
			for (int k = 0; k < 2; k++) {
				CFStringRef key = CFStringCreateWithCString(NULL, keys[k], kCFStringEncodingUTF8);
				CFTypeRef v = IORegistryEntryCreateCFProperty(e, key, kCFAllocatorDefault, 0);
				CFRelease(key);
				if (v && CFGetTypeID(v) == CFDataGetTypeID()) {
					CFIndex len = CFDataGetLength((CFDataRef)v) / 8;
					const uint32_t* p = (const uint32_t*)CFDataGetBytePtr((CFDataRef)v);
					int cnt = 0;
					double mx = 0;
					for (CFIndex j = 0; j < len && cnt < MAXF; j++) {
						if (p[j * 2] == 0) continue;
						g_ftab[k][cnt] = p[j * 2];
						if (g_ftab[k][cnt] > mx) mx = g_ftab[k][cnt];
						cnt++;
					}
					double div = mx > 1e8 ? 1e6 : 1e3; // Hz 또는 kHz(M4 이후)
					for (int j = 0; j < cnt; j++) g_ftab[k][j] /= div;
					g_fn[k] = cnt;
				}
				if (v) CFRelease(v);
			}
		}
		IOObjectRelease(e);
	}
	IOObjectRelease(it);
}

static int ior_init(void) {
	load_ftab();
	CFDictionaryRef ch = IOReportCopyChannelsInGroup(CFSTR("CPU Stats"), CFSTR("CPU Core Performance States"), 0, 0, 0);
	if (!ch) return -1;
	CFMutableDictionaryRef m = CFDictionaryCreateMutableCopy(NULL, 0, ch);
	CFRelease(ch);
	g_sub = IOReportCreateSubscription(NULL, m, &g_subch, 0, NULL);
	if (!g_sub) return -2;
	g_prev = IOReportCreateSamples(g_sub, g_subch, NULL);
	return g_prev ? 0 : -3;
}

static double ior_fmin(int kind) { return g_fn[kind] ? g_ftab[kind][0] : 0; }
static double ior_fmax(int kind) { return g_fn[kind] ? g_ftab[kind][g_fn[kind] - 1] : 0; }
static int ior_fsteps(int kind) { return g_fn[kind]; }

typedef struct {
	int kind;      // 0=E, 1=P
	int cluster;
	int core;
	double mhz;    // 활성 상태에서의 평균 주파수
	double active; // 활성 상태 비율(0~1)
} corefreq;

static int ior_sample(corefreq* out, int max) {
	if (!g_sub || !g_prev) return -1;
	CFDictionaryRef cur = IOReportCreateSamples(g_sub, g_subch, NULL);
	if (!cur) return -1;
	CFDictionaryRef d = IOReportCreateSamplesDelta(g_prev, cur, NULL);
	CFRelease(g_prev);
	g_prev = cur;
	if (!d) return -1;

	int n = 0;
	CFArrayRef arr = CFDictionaryGetValue(d, CFSTR("IOReportChannels"));
	if (arr) {
		for (CFIndex i = 0; i < CFArrayGetCount(arr) && n < max; i++) {
			CFDictionaryRef c = CFArrayGetValueAtIndex(arr, i);
			CFStringRef nm = IOReportChannelGetChannelName(c);
			char name[32] = {0};
			if (!nm || !CFStringGetCString(nm, name, sizeof name, kCFStringEncodingUTF8)) continue;
			int kind;
			if (strncmp(name, "ECPU", 4) == 0) kind = 0;
			else if (strncmp(name, "PCPU", 4) == 0) kind = 1;
			else continue;
			const char* rest = name + 4;
			int cl = 0, co = 0;
			if (strlen(rest) >= 3) { cl = rest[0] - '0'; co = rest[1] - '0'; }
			else co = atoi(rest);

			int sc = IOReportStateGetCount(c);
			double total = 0, act = 0, wsum = 0;
			int j = 0;
			for (int s = 0; s < sc; s++) {
				double r = (double)IOReportStateGetResidency(c, s);
				total += r;
				char sn[16] = {0};
				CFStringRef sref = IOReportStateGetNameForIndex(c, s);
				if (sref) CFStringGetCString(sref, sn, sizeof sn, kCFStringEncodingUTF8);
				if (!strcmp(sn, "IDLE") || !strcmp(sn, "DOWN") || !strcmp(sn, "OFF")) continue;
				double f = 0;
				if (g_fn[kind] > 0) f = g_ftab[kind][j < g_fn[kind] ? j : g_fn[kind] - 1];
				act += r;
				wsum += r * f;
				j++;
			}
			out[n].kind = kind;
			out[n].cluster = cl;
			out[n].core = co;
			out[n].mhz = act > 0 ? wsum / act : 0;
			out[n].active = total > 0 ? act / total : 0;
			n++;
		}
	}
	CFRelease(d);
	return n;
}

// ---- 프로세스 ----
typedef struct {
	int pid, ppid, threads, ok;
	unsigned uid;
	char name[64];
	uint64_t cpu_ns, pcpu_ns, energy_nj, footprint, max_footprint;
	uint64_t instr, cycles, pcycles;
	uint64_t qos[7]; // 기본, 유지보수, 백그라운드, 유틸리티, 레거시, 사용자 시작, 사용자 상호작용 (ns)
} procraw;

static double g_tb;

static void tb_init(void) {
	if (g_tb == 0) {
		mach_timebase_info_data_t t;
		mach_timebase_info(&t);
		g_tb = (double)t.numer / t.denom;
	}
}

static int proc_list(int* pids, int cap) {
	int n = proc_listallpids(pids, sizeof(int) * cap);
	return n < 0 ? -1 : n;
}

// proc_usage 는 프로세스 하나의 누적 사용량을 읽는다. 권한이 없으면 0 을 돌려준다.
static int proc_usage(int pid, procraw* p) {
	memset(p, 0, sizeof *p);
	p->pid = pid;
	struct rusage_info_v6 ri;
	if (proc_pid_rusage(pid, RUSAGE_INFO_V6, (rusage_info_t*)&ri) != 0) return 0;
	p->ok = 1;
	p->cpu_ns = (uint64_t)((ri.ri_user_time + ri.ri_system_time) * g_tb);
	p->pcpu_ns = (uint64_t)((ri.ri_user_ptime + ri.ri_system_ptime) * g_tb);
	p->energy_nj = ri.ri_energy_nj;
	p->footprint = ri.ri_phys_footprint;
	p->max_footprint = ri.ri_lifetime_max_phys_footprint;
	p->instr = ri.ri_instructions;
	p->cycles = ri.ri_cycles;
	p->pcycles = ri.ri_pcycles;
	uint64_t q[7] = {ri.ri_cpu_time_qos_default, ri.ri_cpu_time_qos_maintenance,
		ri.ri_cpu_time_qos_background, ri.ri_cpu_time_qos_utility, ri.ri_cpu_time_qos_legacy,
		ri.ri_cpu_time_qos_user_initiated, ri.ri_cpu_time_qos_user_interactive};
	for (int j = 0; j < 7; j++) p->qos[j] = (uint64_t)(q[j] * g_tb);
	return 1;
}

// proc_meta 는 이름 · 부모 · 사용자를 채운다. 처음 보는 프로세스에만 부른다.
static void proc_meta(int pid, procraw* p) {
	struct proc_bsdshortinfo bi;
	if (proc_pidinfo(pid, PROC_PIDT_SHORTBSDINFO, 1, &bi, sizeof bi) == sizeof bi) {
		p->ppid = bi.pbsi_ppid;
		p->uid = bi.pbsi_uid;
		strncpy(p->name, bi.pbsi_comm, sizeof p->name - 1);
	}
	char nm[2 * MAXCOMLEN + 1] = {0};
	if (proc_name(pid, nm, sizeof nm) > 0) strncpy(p->name, nm, sizeof p->name - 1);
}

// proc_path 는 실행 파일 경로다. proc_name 은 32자에서 잘리므로 전체 이름은 여기서 얻는다.
static int proc_path(int pid, char* buf, int len) {
	return proc_pidpath(pid, buf, len);
}

// proc_threads 는 스레드 수다. 비용이 커서 화면에 보이는 줄만 부른다.
static int proc_threads(int pid) {
	struct proc_taskinfo ti;
	if (proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &ti, sizeof ti) == sizeof ti) return ti.pti_threadnum;
	return -1;
}
*/
import "C"

import (
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- 주파수 ----

type CoreFreq struct {
	MHz    float64
	Active float64 // 0~1
}

var freqOK bool

func initFreq() bool {
	freqOK = C.ior_init() == 0
	return freqOK
}

func freqRange(isP bool) (minMHz, maxMHz float64, steps int) {
	k := C.int(0)
	if isP {
		k = 1
	}
	return float64(C.ior_fmin(k)), float64(C.ior_fmax(k)), int(C.ior_fsteps(k))
}

// sampleFreq 는 논리 CPU 순서(E 먼저, 그다음 P)대로 주파수를 돌려준다.
func sampleFreq() []CoreFreq {
	if !freqOK {
		return nil
	}
	var buf [128]C.corefreq
	n := int(C.ior_sample(&buf[0], 128))
	if n <= 0 {
		return nil
	}
	out := make([]CoreFreq, n)
	for i := 0; i < n; i++ {
		out[i] = CoreFreq{MHz: float64(buf[i].mhz), Active: float64(buf[i].active)}
	}
	return out
}

// ---- 프로세스 ----

const maxProcs = 8192

var qosNames = [7]string{"기본", "유지보수", "백그라운드", "유틸리티", "레거시", "사용자 시작", "사용자 상호작용"}

type ProcStat struct {
	PID, PPID   int
	Name, User  string
	Threads     int
	CPU         float64 // 한 코어 기준 % (여러 코어를 쓰면 100 을 넘는다)
	PCPU, ECPU  float64 // 그중 성능 · 효율 코어에서 쓴 몫
	SortCPU     float64 // 정렬용 평균 (anim.go)
	SortP       float64
	SortE       float64
	PShare      float64 // 누적 기준 P 코어 비율(0~1)
	PowerMW     float64
	Mem, MemMax uint64
	CPUTime     time.Duration
	IPC         float64
	PCycleShare float64
	QoS         [7]float64 // 이번 구간(없으면 누적)의 QoS 별 CPU 시간 비율(0~1)
	QoSLive     bool
}

type procMeta struct {
	name, user string
	ppid       int
}

type thrEntry struct{ n, tick int }

type procSampler struct {
	pids    []C.int
	prev    map[int]C.procraw
	prevT   time.Time
	users   map[uint32]string
	meta    map[int]procMeta
	denied  map[int]int      // 권한 없음 → 마지막으로 시도한 회차
	threads map[int]thrEntry // 스레드 수 캐시
	tick    int
}

func newProcSampler() *procSampler {
	C.tb_init()
	return &procSampler{
		pids:    make([]C.int, maxProcs),
		prev:    map[int]C.procraw{},
		users:   map[uint32]string{},
		meta:    map[int]procMeta{},
		denied:  map[int]int{},
		threads: map[int]thrEntry{},
	}
}

// threadCount 는 스레드 수를 5회마다 한 번만 새로 읽는다.
func (s *procSampler) threadCount(pid int) int {
	if e, ok := s.threads[pid]; ok && s.tick-e.tick < 5 {
		return e.n
	}
	n := int(C.proc_threads(C.int(pid)))
	s.threads[pid] = thrEntry{n, s.tick}
	return n
}

func (s *procSampler) userName(uid uint32) string {
	if n, ok := s.users[uid]; ok {
		return n
	}
	n := strconv.Itoa(int(uid))
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	s.users[uid] = n
	return n
}

// sample 은 접근할 수 있는 프로세스 목록과, 권한이 없어 못 읽은 프로세스 수를 돌려준다.
func (s *procSampler) sample() ([]ProcStat, int) {
	now := time.Now()
	s.tick++
	n := int(C.proc_list(&s.pids[0], maxProcs))
	if n < 0 {
		return nil, 0
	}
	raws := make([]C.procraw, 0, n)
	hidden := 0
	denied := make(map[int]int, len(s.denied))
	for _, cp := range s.pids[:n] {
		pid := int(cp)
		// 권한이 없던 프로세스는 10회에 한 번만 다시 시도한다.
		if t, ok := s.denied[pid]; ok && s.tick-t < 10 {
			denied[pid] = t
			hidden++
			continue
		}
		var r C.procraw
		if C.proc_usage(cp, &r) == 0 {
			denied[pid] = s.tick
			hidden++
			continue
		}
		raws = append(raws, r)
	}
	s.denied = denied
	dt := float64(now.Sub(s.prevT).Nanoseconds())
	first := s.prevT.IsZero()

	cur := make(map[int]C.procraw, n)
	meta := make(map[int]procMeta, n)
	out := make([]ProcStat, 0, n)
	for _, r := range raws {
		pid := int(r.pid)
		cur[pid] = r
		md, ok := s.meta[pid]
		if !ok {
			var m C.procraw
			C.proc_meta(C.int(pid), &m)
			md = procMeta{name: fullName(pid, C.GoString(&m.name[0])), user: s.userName(uint32(m.uid)), ppid: int(m.ppid)}
		}
		meta[pid] = md
		ps := ProcStat{
			PID:     pid,
			PPID:    md.ppid,
			Name:    md.name,
			User:    md.user,
			Threads: -1,
			Mem:     uint64(r.footprint),
			MemMax:  uint64(r.max_footprint),
			CPUTime: time.Duration(r.cpu_ns),
		}
		if r.cpu_ns > 0 {
			ps.PShare = float64(r.pcpu_ns) / float64(r.cpu_ns)
		}
		if r.cycles > 0 {
			ps.IPC = float64(r.instr) / float64(r.cycles)
			ps.PCycleShare = float64(r.pcycles) / float64(r.cycles)
		}

		p, had := s.prev[pid]
		var qd [7]float64
		var qsum float64
		if had && !first && dt > 0 && r.cpu_ns >= p.cpu_ns {
			dc := float64(r.cpu_ns - p.cpu_ns)
			dp := float64(sub(r.pcpu_ns, p.pcpu_ns))
			if dp > dc {
				dp = dc
			}
			ps.CPU = dc / dt * 100
			ps.PCPU = dp / dt * 100
			ps.ECPU = ps.CPU - ps.PCPU
			ps.PowerMW = float64(sub(r.energy_nj, p.energy_nj)) / dt * 1000
			for j := 0; j < 7; j++ {
				qd[j] = float64(sub(r.qos[j], p.qos[j]))
				qsum += qd[j]
			}
		}
		if qsum > 0 {
			ps.QoSLive = true
		} else {
			qsum = 0
			for j := 0; j < 7; j++ {
				qd[j] = float64(r.qos[j])
				qsum += qd[j]
			}
		}
		if qsum > 0 {
			for j := 0; j < 7; j++ {
				ps.QoS[j] = qd[j] / qsum
			}
		}
		out = append(out, ps)
	}
	s.prev = cur
	s.meta = meta
	for pid := range s.threads {
		if _, ok := cur[pid]; !ok {
			delete(s.threads, pid)
		}
	}
	s.prevT = now
	return out, hidden
}

func sub(a, b C.uint64_t) uint64 {
	if a < b {
		return 0
	}
	return uint64(a - b)
}

// fullName 은 잘리지 않은 프로세스 이름을 돌려준다.
// proc_name 이 경로의 파일 이름 앞부분과 같으면 경로의 파일 이름을 쓴다.
func fullName(pid int, short string) string {
	var buf [4096]C.char
	if C.proc_path(C.int(pid), &buf[0], C.int(len(buf))) <= 0 {
		return short
	}
	base := filepath.Base(C.GoString(&buf[0]))
	if short == "" || strings.HasPrefix(base, short) {
		return base
	}
	return short
}
