package main

import (
	"testing"
	"time"
)

func setup() model {
	m := newModel()
	m.width, m.height = 140, 50
	time.Sleep(200 * time.Millisecond)
	m.refresh()
	return m
}
func BenchmarkCPU(b *testing.B) {
	m := setup()
	for i := 0; i < b.N; i++ {
		m.cpuS.sample()
	}
}
func BenchmarkMem(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sampleMem()
		sampleSys()
	}
}
func BenchmarkFreq(b *testing.B) {
	setup()
	for i := 0; i < b.N; i++ {
		sampleFreq()
	}
}
func BenchmarkProcs(b *testing.B) {
	m := setup()
	for i := 0; i < b.N; i++ {
		m.procS.sample()
	}
}
func BenchmarkViewOverview(b *testing.B) {
	m := setup()
	m.tab = tabOverview
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
func BenchmarkViewCores(b *testing.B) {
	m := setup()
	m.tab = tabCores
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
func BenchmarkViewProcs(b *testing.B) {
	m := setup()
	m.tab = tabProcs
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
