package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/sys/unix"
)

// Level 은 코어 종류(성능 · 효율) 하나의 사양이다.
type Level struct {
	Count      int
	PerL2      int // 클러스터 하나의 코어 수 (L2 캐시를 같이 쓰는 묶음)
	L1i, L1d   uint64
	L2         uint64
	FMin, FMax float64 // MHz
	FSteps     int
}

func (l Level) Clusters() int {
	if l.PerL2 <= 0 {
		return 1
	}
	return (l.Count + l.PerL2 - 1) / l.PerL2
}

// CPUInfo 는 칩 이름과 코어 구성이다.
// Apple Silicon 은 논리 CPU 번호가 효율 코어(E)부터 매겨진다.
type CPUInfo struct {
	Model   string
	Logical int
	P, E    Level
}

func loadLevel(n int) (Level, bool) {
	pre := "hw.perflevel" + string(rune('0'+n)) + "."
	cnt, err := unix.SysctlUint32(pre + "logicalcpu")
	if err != nil {
		return Level{}, false
	}
	u32 := func(k string) uint64 {
		v, _ := unix.SysctlUint32(pre + k)
		return uint64(v)
	}
	return Level{
		Count: int(cnt),
		PerL2: int(u32("cpusperl2")),
		L1i:   u32("l1icachesize"),
		L1d:   u32("l1dcachesize"),
		L2:    u32("l2cachesize"),
	}, true
}

func loadCPUInfo() CPUInfo {
	info := CPUInfo{}
	if s, err := unix.Sysctl("machdep.cpu.brand_string"); err == nil {
		info.Model = strings.TrimSpace(s)
	}
	if n, err := unix.SysctlUint32("hw.logicalcpu"); err == nil {
		info.Logical = int(n)
	}
	p, pok := loadLevel(0)
	e, eok := loadLevel(1)
	if pok && eok && p.Count+e.Count == info.Logical {
		info.P, info.E = p, e
	} else {
		info.P = Level{Count: info.Logical, PerL2: info.Logical} // 인텔 맥 등: 한 묶음으로 본다
	}
	if initFreq() {
		info.P.FMin, info.P.FMax, info.P.FSteps = freqRange(true)
		info.E.FMin, info.E.FMax, info.E.FSteps = freqRange(false)
	}
	return info
}

// IsE 는 논리 CPU i 가 효율 코어인지 알려준다.
func (c CPUInfo) IsE(i int) bool { return i < c.E.Count }

// CoreLabel 은 화면에 쓰는 코어 이름(E1, P7 …)과 클러스터 번호다.
func (c CPUInfo) CoreLabel(i int) (string, int) {
	if c.IsE(i) {
		return "E" + itoa(i+1), i / max(c.E.PerL2, 1)
	}
	j := i - c.E.Count
	return "P" + itoa(j+1), j / max(c.P.PerL2, 1)
}

func itoa(n int) string { return strconv.Itoa(n) }

// Usage 는 한 구간 동안의 사용률(%)이다.
type Usage struct {
	User, Sys, Total float64
}

type cpuSampler struct {
	prev []cpu.TimesStat
}

func (s *cpuSampler) sample() ([]Usage, Usage, error) {
	cur, err := cpu.Times(true)
	if err != nil {
		return nil, Usage{}, err
	}
	prev := s.prev
	s.prev = cur
	if len(prev) != len(cur) {
		return nil, Usage{}, nil
	}

	cores := make([]Usage, len(cur))
	var sumU, sumS, sumAll float64
	for i := range cur {
		u := cur[i].User + cur[i].Nice - prev[i].User - prev[i].Nice
		sy := cur[i].System - prev[i].System
		id := cur[i].Idle - prev[i].Idle
		all := u + sy + id
		if all <= 0 {
			continue
		}
		cores[i] = Usage{User: u / all * 100, Sys: sy / all * 100, Total: (u + sy) / all * 100}
		sumU += u
		sumS += sy
		sumAll += all
	}
	var total Usage
	if sumAll > 0 {
		total = Usage{User: sumU / sumAll * 100, Sys: sumS / sumAll * 100, Total: (sumU + sumS) / sumAll * 100}
	}
	return cores, total, nil
}

// MemInfo 는 활성 상태 보기(Activity Monitor)와 같은 방식으로 나눈 메모리 값(바이트)이다.
type MemInfo struct {
	Total, Used                    uint64
	App, Wired, Compressed, Cached uint64
	Free                           uint64
	SwapTotal, SwapUsed            uint64
	Pressure                       int // 1 정상, 2 주의, 4 위험
}

func (m MemInfo) UsedPct() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Used) / float64(m.Total) * 100
}

func sampleMem() (MemInfo, error) {
	var mi MemInfo
	vm, err := mem.VirtualMemory()
	if err != nil {
		return mi, err
	}
	ps := uint64(os.Getpagesize())
	pages := func(name string) uint64 {
		v, err := unix.SysctlUint32(name)
		if err != nil {
			return 0
		}
		return uint64(v)
	}
	internal := pages("vm.page_pageable_internal_count")
	purgeable := pages("vm.page_purgeable_count")
	external := pages("vm.page_pageable_external_count")
	compressed, _ := unix.SysctlUint64("vm.compressor_bytes_used")

	mi.Total = vm.Total
	if internal > purgeable {
		mi.App = (internal - purgeable) * ps
	}
	mi.Wired = vm.Wired
	mi.Compressed = compressed
	mi.Cached = (external + purgeable) * ps
	mi.Used = mi.App + mi.Wired + mi.Compressed
	if mi.Used > mi.Total {
		mi.Used = mi.Total
	}
	if mi.Used+mi.Cached < mi.Total {
		mi.Free = mi.Total - mi.Used - mi.Cached
	} else {
		mi.Cached = mi.Total - mi.Used
	}

	if sw, err := mem.SwapMemory(); err == nil {
		mi.SwapTotal, mi.SwapUsed = sw.Total, sw.Used
	}
	if p, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level"); err == nil {
		mi.Pressure = int(p)
	}
	return mi, nil
}

// SysInfo 는 가동 시간과 부하 평균이다.
type SysInfo struct {
	Uptime time.Duration
	Load   [3]float64
}

func sampleSys() SysInfo {
	var si SysInfo
	if tv, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		si.Uptime = time.Since(time.Unix(tv.Sec, int64(tv.Usec)*1000))
	}
	if l, err := load.Avg(); err == nil {
		si.Load = [3]float64{l.Load1, l.Load5, l.Load15}
	}
	return si
}
