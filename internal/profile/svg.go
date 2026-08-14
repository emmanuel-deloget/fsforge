package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The chart is two stacked plots sharing one time axis, rather than two scales
// on one plot. Two vertical scales on a single set of axes let the author put
// the crossing wherever a chosen bound happens to place it, and a reader takes
// that accident for a relationship. Stacked, the simultaneity is still legible —
// what happens at 5.7 s in one band and in the other — and no proportion between
// them is implied.
//
// It is emitted as a standalone SVG with no script, so it can sit in a README:
// GitHub strips anything else, and a chart in documentation should not need a
// runtime to be read.

const (
	svgW, svgH = 900.0, 430.0
	padL, padR = 66.0, 22.0
	padT, padB = 26.0, 54.0
	bandGap    = 52.0
)

// writeSVG renders a run and writes it to path.
func writeSVG(run Run, path string) error {
	if len(run.Samples) < 2 {
		return fmt.Errorf("profile: need at least two samples to draw a chart")
	}
	var b bytes.Buffer
	drawSVG(&b, run)
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func drawSVG(w *bytes.Buffer, run Run) {
	s := run.Samples
	tMax := s[len(s)-1].T
	bandH := (svgH - padT - padB - bandGap) / 2

	// Round the axes out to something a reader can hold in their head.
	memMax := niceCeil(float64(maxLive(s))/MiB, 4)
	ioMax := niceCeil(float64(s[len(s)-1].Written)/GiB, 4)

	x := func(t float64) float64 { return padL + t/tMax*(svgW-padL-padR) }
	yMem := func(v uint64) float64 { return padT + bandH - float64(v)/MiB/memMax*bandH }
	ioTop := padT + bandH + bandGap
	yIO := func(v int64) float64 { return ioTop + bandH - float64(v)/GiB/ioMax*bandH }

	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" class="fsforge-chart" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="t d">`,
		svgW, svgH, svgW, svgH)
	fmt.Fprintf(w, `<title id="t">Memory held and bytes written while fsforge builds a %s image</title>`, run.Filesystem)
	fmt.Fprintf(w, `<desc id="d">Two stacked plots sharing a time axis. Memory held rises to %.0f MiB while the tree is built, then stays flat as %.2f GiB are written.</desc>`,
		float64(run.PeakLive)/MiB, float64(run.BytesWritten)/GiB)

	// The chart has two homes — a README, where it is an <img> in its own
	// document, and a page that inlines it into the DOM — and the colour scoping
	// has to serve both.
	//
	// Three rules follow from that. The custom properties are prefixed, so they
	// cannot collide with a host page's. They are declared on the chart's own
	// class rather than on :root, because an inlined SVG's :root *is* the host
	// document and the chart would overwrite the page's tokens. And the defaults
	// sit in a cascade layer, so any unlayered rule on the page wins without
	// having to out-specify anything: a page that wants the chart to follow its
	// theme toggle writes `.fsforge-chart { --fsf-ink: var(--its-own-token) }`
	// and that is the whole integration.
	//
	// --fsf-bg is a property like the others, so setting it to `transparent`
	// drops the background without a build step or a second file.
	w.WriteString(`<style>
    @layer fsforge-chart{
      .fsforge-chart{--fsf-ink:#1a1815;--fsf-ink-2:#575148;--fsf-ink-3:#8a8378;--fsf-rule:#e2ded6;--fsf-bg:#fbfaf8;--fsf-mem:#2a78d6;--fsf-io:#eb6834;--fsf-mem-fill:#2a78d61c;--fsf-io-fill:#eb68341c}
      @media (prefers-color-scheme:dark){.fsforge-chart{--fsf-ink:#f2efe9;--fsf-ink-2:#b4ada2;--fsf-ink-3:#857e74;--fsf-rule:#34312d;--fsf-bg:#131211;--fsf-mem:#3987e5;--fsf-io:#d95926;--fsf-mem-fill:#3987e524;--fsf-io-fill:#d9592624}}
    }
    .bg{fill:var(--fsf-bg,#fbfaf8)}
    .g{stroke:var(--fsf-rule,#e2ded6);stroke-width:1}
    .tick{font:11px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;fill:var(--fsf-ink-3,#8a8378)}
    .band{font:600 12px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;fill:var(--fsf-ink,#1a1815)}
    .note{font:11px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;fill:var(--fsf-ink-2,#575148)}
    .mark{stroke:var(--fsf-ink-3,#8a8378);stroke-width:1;stroke-dasharray:3 4;opacity:.5}
    .lmem{fill:none;stroke:var(--fsf-mem,#2a78d6);stroke-width:2;stroke-linejoin:round}
    .lio{fill:none;stroke:var(--fsf-io,#eb6834);stroke-width:2;stroke-linejoin:round}
    .hold{stroke:var(--fsf-mem,#2a78d6);stroke-width:1;stroke-dasharray:2 3;opacity:.55}
  </style>`)
	fmt.Fprintf(w, `<rect class="bg" width="%.0f" height="%.0f"/>`, svgW, svgH)

	// Grids and value ticks, four steps apiece.
	for i := 0; i <= 4; i++ {
		v := memMax * float64(i) / 4
		y := padT + bandH - v/memMax*bandH
		fmt.Fprintf(w, `<line class="g" x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f"/>`, padL, y, svgW-padR, y)
		fmt.Fprintf(w, `<text class="tick" x="%.0f" y="%.1f" text-anchor="end">%s</text>`,
			padL-8, y+4, tickLabel(v, i == 4, "MiB"))

		v = ioMax * float64(i) / 4
		y = ioTop + bandH - v/ioMax*bandH
		fmt.Fprintf(w, `<line class="g" x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f"/>`, padL, y, svgW-padR, y)
		fmt.Fprintf(w, `<text class="tick" x="%.0f" y="%.1f" text-anchor="end">%s</text>`,
			padL-8, y+4, tickLabel(v, i == 4, "GiB"))
	}

	// Time ticks along the bottom.
	for _, t := range timeTicks(tMax) {
		label := fmt.Sprintf("%g", t)
		if t == tMax {
			label = fmt.Sprintf("%.1f s", t)
		}
		fmt.Fprintf(w, `<text class="tick" x="%.1f" y="%.0f" text-anchor="middle">%s</text>`,
			x(t), svgH-30, label)
	}

	// The two moments worth naming: when the tree is finished and writing
	// starts, and when the corpus stops being small files.
	for _, m := range transitions(s, run.Corpus) {
		fmt.Fprintf(w, `<line class="mark" x1="%.1f" y1="%.0f" x2="%.1f" y2="%.1f"/>`,
			x(m.t), padT, x(m.t), ioTop+bandH)
		fmt.Fprintf(w, `<text class="note" x="%.1f" y="%.0f">%s</text>`, x(m.t)+5, padT+12, m.label)
	}

	// Areas, then lines on top of them.
	fmt.Fprintf(w, `<path fill="var(--fsf-mem-fill,#2a78d61c)" d="%s"/>`,
		area(s, x, func(p Sample) float64 { return yMem(p.Live) }, padT+bandH))
	fmt.Fprintf(w, `<path fill="var(--fsf-io-fill,#eb68341c)" d="%s"/>`,
		area(s, x, func(p Sample) float64 { return yIO(p.Written) }, ioTop+bandH))
	fmt.Fprintf(w, `<path class="lmem" d="%s"/>`,
		line(s, x, func(p Sample) float64 { return yMem(p.Live) }))
	fmt.Fprintf(w, `<path class="lio" d="%s"/>`,
		line(s, x, func(p Sample) float64 { return yIO(p.Written) }))

	// Band titles double as the legend: one series each, so a legend box would
	// only repeat what the title says. The words stay in ink and a swatch beside
	// them carries the colour — text tinted with the series hue turns illegible
	// the moment a host page puts it on a background the palette did not expect.
	bandLabel(w, padL, padT-13, "memory held", "var(--fsf-mem,#2a78d6)")
	bandLabel(w, padL, ioTop-13, "bytes written", "var(--fsf-io,#eb6834)")

	// The flat stretch, marked where it is rather than only described in words:
	// the point of the chart is that this line does not move while the one below
	// it climbs.
	plateau := plateauLevel(s)
	if plateau > 0 {
		y := yMem(plateau)
		fmt.Fprintf(w, `<line class="hold" x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f"/>`,
			padL, y, svgW-padR, y)
		// Anchored at the left, where the curve is still climbing well below the
		// plateau: on the right it would sit on top of the line it describes.
		fmt.Fprintf(w, `<text class="note" x="%.0f" y="%.1f">held: %.0f MiB</text>`,
			padL+6, y-6, float64(plateau)/MiB)
	}

	// The headline, stated once where the flat stretch is.
	fmt.Fprintf(w, `<text class="note" x="%.0f" y="%.0f" text-anchor="end">%s</text>`,
		svgW-padR, padT-9, summary(run))
	// Provenance sits on its own line under the axis. Sharing the axis row with
	// the time ticks hid half of them behind it.
	fmt.Fprintf(w, `<text class="tick" x="%.0f" y="%.0f" text-anchor="end">%s</text>`,
		svgW-padR, svgH-8, provenance(run))

	w.WriteString(`</svg>` + "\n")
}

// bandLabel draws a swatch and a title: identity in the mark, words in ink.
func bandLabel(w *bytes.Buffer, x, y float64, text, colour string) {
	fmt.Fprintf(w, `<rect x="%.0f" y="%.0f" width="14" height="3" rx="1.5" fill="%s"/>`, x, y, colour)
	fmt.Fprintf(w, `<text class="band" x="%.0f" y="%.0f">%s</text>`, x+20, y+4, text)
}

// --- helpers ---

const (
	MiB = 1 << 20
	GiB = 1 << 30
)

type transition struct {
	t     float64
	label string
}

// transitions marks the two changes of regime.
//
// The first is measured: the sample where the first byte reaches the device,
// which is where the tree stops being built and starts being written.
//
// The second is derived from the corpus rather than inferred from the curve.
// The strata are walked in directory order, so the large files begin once the
// smaller strata's bytes have gone out; reading that boundary off the plan is
// exact, where guessing it from a change of slope moves around between runs and
// tells the reader something that is not quite true.
func transitions(s []Sample, corpus CorpusStats) []transition {
	var out []transition
	for _, p := range s {
		if p.Written > 0 {
			out = append(out, transition{p.T, "tree built"})
			break
		}
	}
	if len(out) == 0 || len(corpus.Strata) < 2 {
		return out
	}

	// Bytes belonging to every stratum but the last.
	var beforeLast int64
	for _, st := range corpus.Strata[:len(corpus.Strata)-1] {
		beforeLast += st.TargetBytes
	}
	for _, p := range s {
		if p.Written >= beforeLast && p.T > out[0].t {
			out = append(out, transition{p.T, corpus.Strata[len(corpus.Strata)-1].Name + " files"})
			break
		}
	}
	return out
}

func line(s []Sample, x func(float64) float64, y func(Sample) float64) string {
	var b strings.Builder
	for i, p := range s {
		verb := "L"
		if i == 0 {
			verb = "M"
		}
		fmt.Fprintf(&b, "%s%.1f %.1f", verb, x(p.T), y(p))
	}
	return b.String()
}

func area(s []Sample, x func(float64) float64, y func(Sample) float64, base float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "M%.1f %.1f", x(s[0].T), base)
	for _, p := range s {
		fmt.Fprintf(&b, "L%.1f %.1f", x(p.T), y(p))
	}
	fmt.Fprintf(&b, "L%.1f %.1fZ", x(s[len(s)-1].T), base)
	return b.String()
}

// plateauLevel is the memory held once the tree is built — the median of the
// second half, so a late blip does not stand in for the level.
func plateauLevel(s []Sample) uint64 {
	var tail []uint64
	for _, p := range s[len(s)/2:] {
		tail = append(tail, p.Live)
	}
	if len(tail) == 0 {
		return 0
	}
	sort.Slice(tail, func(a, b int) bool { return tail[a] < tail[b] })
	return tail[len(tail)/2]
}

func maxLive(s []Sample) uint64 {
	var m uint64
	for _, p := range s {
		if p.Live > m {
			m = p.Live
		}
	}
	return m
}

// niceCeil rounds up to a value that divides cleanly into steps.
//
// The magnitude is walked in both directions: an axis topping out below one —
// a small image, a short run — was being rounded up to 1 and drawn with the
// data squashed into its bottom tenth.
func niceCeil(v float64, steps int) float64 {
	if v <= 0 {
		return 1
	}
	mag := 1.0
	for v/mag > 10 {
		mag *= 10
	}
	for v/mag < 1 && mag > 1e-9 {
		mag /= 10
	}
	for _, m := range []float64{1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if v <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

func tickLabel(v float64, last bool, unit string) string {
	s := fmt.Sprintf("%g", round1(v))
	if last {
		return s + " " + unit
	}
	return s
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func timeTicks(tMax float64) []float64 {
	step := niceCeil(tMax/5, 1)
	var out []float64
	for t := 0.0; t < tMax-step/2; t += step {
		out = append(out, round1(t))
	}
	return append(out, round1(tMax))
}

// summary is the sentence the chart exists to make: memory does not follow
// volume.
func summary(run Run) string {
	return fmt.Sprintf("%.0f MiB held while %.1f GiB written · %.0f B per file",
		float64(run.PeakLive)/MiB,
		float64(run.BytesWritten)/GiB,
		float64(run.PeakLive)/float64(max(1, run.Corpus.Files)))
}

func provenance(run Run) string {
	cpu := run.Host.CPU
	if cpu == "" {
		cpu = run.Host.GOARCH
	}
	cores := fmt.Sprintf("%d cores", run.Host.MaxProcs)
	if run.Host.MaxProcs != run.Host.NumCPU {
		cores = fmt.Sprintf("%d of %d cores", run.Host.MaxProcs, run.Host.NumCPU)
	}
	return fmt.Sprintf("%s · %s files, %.1f GiB · %s, %s · %s %s",
		run.Filesystem, thousands(run.Corpus.Files), float64(run.Corpus.Bytes)/GiB,
		cpu, cores, run.Host.GOOS, run.Host.Version)
}

// thousands groups digits so a file count reads at a glance.
func thousands(n int) string {
	s := fmt.Sprint(n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}
