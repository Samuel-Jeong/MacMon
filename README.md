```text
      ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃
    ┏━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━┻━━━┓
  ━━┫                                                                    ┣━━
  ━━┫   ███╗   ███╗  █████╗   ██████╗ ███╗   ███╗  ██████╗  ███╗   ██╗   ┣━━
  ━━┫   ████╗ ████║ ██╔══██╗ ██╔════╝ ████╗ ████║ ██╔═══██╗ ████╗  ██║   ┣━━
  ━━┫   ██╔████╔██║ ███████║ ██║      ██╔████╔██║ ██║   ██║ ██╔██╗ ██║   ┣━━
  ━━┫   ██║╚██╔╝██║ ██╔══██║ ██║      ██║╚██╔╝██║ ██║   ██║ ██║╚██╗██║   ┣━━
  ━━┫   ██║ ╚═╝ ██║ ██║  ██║ ╚██████╗ ██║ ╚═╝ ██║ ╚██████╔╝ ██║ ╚████║   ┣━━
  ━━┫   ╚═╝     ╚═╝ ╚═╝  ╚═╝  ╚═════╝ ╚═╝     ╚═╝  ╚═════╝  ╚═╝  ╚═══╝   ┣━━
  ━━┫                                                                    ┣━━
  ━━┫   P  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━╸┄┄┄┄┄┄┄┄  78%  performance  ┣━━
  ━━┫   E  ━━━━━━━━━━━━━╸┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄  31%  efficiency   ┣━━
  ━━┫                                                                    ┣━━
  ━━┫   ▆▇▇▇▆▆▅▅▅▅▅▅▅▄▃▂▁▁▂▄▅▇███▇▆▄▄▄▄▄▄▄▄▃▂▂▂▃▄▆▇███▇▅▄▃▂▂▃▃▄▄▄▄▃▃▄▅   ┣━━
  ━━┫                                                                    ┣━━
    ┗━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━┳━━━┛
      ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃  ┃

           P-core / E-core aware system monitor for Apple Silicon
```

# MacMon

MacMon is a terminal monitor for Apple Silicon Macs. It shows CPU cores, memory, and processes,
splits everything into performance (P) and efficiency (E) cores, and tells you how much of each
process runs on which kind of core. It runs without `sudo`.

## Requirements

- An Apple Silicon Mac (M1 or later). Tested on an M3 Max with macOS 27.2.
- Go 1.26 or later
- Xcode Command Line Tools (`xcode-select --install`). MacMon calls macOS APIs through cgo.

## Build and run

```bash
go build -o macmon .
```

```bash
./macmon
```

To include processes owned by root and other users (about 200 on a typical system), run it with `sudo`:

```bash
sudo ./macmon
```

## Screens

Switch screens with `tab` or the number keys.

| Screen | What it shows |
|---|---|
| 1 Overview | Total CPU usage with a recent history graph, per-core usage and frequency, memory (app · wired · compressed · cached · free), swap, memory pressure, and the top CPU consumers |
| 2 Cores | Chip specs (L1/L2 cache, frequency range and number of steps), and for each cluster: per-core user/system usage, current frequency, and recent history |
| 3 Processes | Every process with CPU, P-core and E-core usage, a P/E usage bar, threads, memory, and power. The selected process also shows its lifetime P/E split, QoS breakdown, and IPC |

MacMon uses the full width of the terminal. When the window is too narrow, the process table drops
the user, power, and thread columns first, and only shortens process names as a last resort.

## Keys

| Key | Action |
|---|---|
| `tab` · `shift+tab` · `←` `→` | Switch screens |
| `1` `2` `3` | Jump to a screen |
| `↑` `↓` (`k` `j`) | Select a process, or scroll on other screens |
| `PgUp` `PgDn` · `Home` `End` | Move a page · jump to the first or last row |
| `s` · `S` | Change the sort order (CPU → P-core → E-core → memory → power → PID → name) |
| `c` | Switch the CPU% unit (one core = 100% ↔ whole Mac = 100%) |
| `a` | Turn smooth animation on or off |
| `p` · `space` | Pause |
| `+` · `-` | Slower or faster refresh (0.25 · 0.5 · 1 · 2 · 5 s, default 1 s) |
| `q` · `esc` | Quit |

## Reading the numbers

**CPU%** uses the same scale as Activity Monitor and `top` by default: one core = 100%. A process that
uses several cores goes above 100% (up to 1600% on a 16-core Mac). Press `c` to switch to
whole Mac = 100%. The current unit is shown at the top right of the process table.

**P-core and E-core** show how much CPU the process used on performance and efficiency cores.
They add up to CPU%. In the P/E usage bar, the full column width is one core (100%);
blue is P-core time and purple is E-core time.

**QoS** is the priority class macOS assigns to work. Background and maintenance work runs only on
efficiency cores. To send a command to the efficiency cores, run it like this:

```bash
taskpolicy -b <command>
```

**Memory** is split the same way as in Activity Monitor: used = app + wired + compressed.
Cached memory can be reclaimed immediately when needed.

## Smooth animation

Numbers are averaged over the refresh interval, so they change only once per interval (once a second by default).
Bars and graphs sample CPU separately every quarter of the interval (0.25 s by default) and glide
to each new value at 20 frames per second. Process order is based on a few seconds' average, so rows
do not jump around every second. Tune the animation speed with `easeTau` in [anim.go](anim.go).

## Overhead

Measured over 30 seconds with a 1 s refresh, as a percentage of one core.
Divide by 16 for the share of the whole Mac.

| Screen | Smooth on | Smooth off |
|---|---|---|
| Overview | ~2.0% | ~1.0% |
| Cores | ~3.3% | — |
| Processes | ~0.5% | — |

The process screen has no moving bars, so it does not run the animation. Under the same conditions,
macOS `top` used about 7%. Most of the cost comes from reading usage for roughly 750 processes and
reading core frequencies. Data that is not on screen is not collected.

## Limitations

- macOS does not expose which core a process is running on right now. MacMon shows how much time
  each process spent on P-cores and E-cores, not individual core numbers.
- Without `sudo`, processes owned by other users (root and so on) are left out. The table shows how many.
- macOS reports efficiency-core frequency per cluster, so all four E-cores show the same frequency.
- Core frequencies come from a private API (IOReport). If a future macOS changes it,
  only the frequency fields will show as unavailable.
- Intel Macs are untested. All cores would likely show as one group, without frequencies.
- At startup MacMon asks the terminal for its background color. Terminals that do not answer
  this query may delay startup by a few seconds.

## Data sources

| Value | Source |
|---|---|
| Core usage | `host_processor_info` (gopsutil) |
| Core frequency | IOReport `CPU Stats` / `CPU Core Performance States`, plus the frequency table in the IORegistry `pmgr` node |
| Chip specs | `sysctl hw.perflevel0.*` (performance), `hw.perflevel1.*` (efficiency) |
| Process CPU, P/E time, QoS, power, memory | `proc_pid_rusage(RUSAGE_INFO_V6)` (`ri_user_ptime`, `ri_cpu_time_qos_*`, `ri_energy_nj`, …) |
| Process name, parent, user, threads | `proc_pidpath`, `proc_pidinfo` |
| Memory | sysctl (`vm.page_pageable_internal_count`, …) and gopsutil |

## Project layout

| File | Contents |
|---|---|
| [main.go](main.go) | Entry point, state, key handling, refresh timing |
| [views.go](views.go) | The three screens |
| [render.go](render.go) | Colors, bars, graphs, boxes |
| [anim.go](anim.go) | Smooth animation and the averaged process sort |
| [darwin.go](darwin.go) | macOS API calls via cgo: core frequencies, process usage |
| [stats.go](stats.go) | Chip specs, core usage, memory, uptime and load |
| [bench_test.go](bench_test.go) | Per-stage cost benchmarks |

## Development

Render all three screens once and exit, without a terminal:

```bash
./macmon -snapshot 120x45
```

Add `CLICOLOR_FORCE=1` in front to keep the colors.

Measure the cost of each stage:

```bash
go test -run XXX -bench . -benchmem .
```

Write a CPU profile while running. The file is written when you quit with `q`:

```bash
MACMON_CPUPROFILE=cpu.prof ./macmon
```
