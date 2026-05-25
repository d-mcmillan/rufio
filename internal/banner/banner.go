// Package banner renders the Rufio CLI splash banner. Mirrors src/banner.ts.
//
// The wordmark uses a 5-stop gradient (one colour per letter of RUFIO):
// pink → coral → amber → spring-green → cyan. Below the wordmark, a
// half-red / half-amber hairline is the Hook bandana easter-egg. Plain
// fallback for non-TTY / NO_COLOR=1.
package banner

import (
	"fmt"
	"os"
	"strings"
)

// Options controls banner rendering. Zero-value gets sensible defaults.
type Options struct {
	Version     string
	Tagline     string
	ShowVersion bool
}

const defaultTagline = "the substrate for distributed cognition"

var wordmark = []string{
	"██████╗ ██╗   ██╗███████╗██╗ ██████╗ ",
	"██╔══██╗██║   ██║██╔════╝██║██╔═══██╗",
	"██████╔╝██║   ██║█████╗  ██║██║   ██║",
	"██╔══██╗██║   ██║██╔══╝  ██║██║   ██║",
	"██║  ██║╚██████╔╝██║     ██║╚██████╔╝",
	"╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝ ╚═════╝ ",
}

// 5 gradient stops, one per letter of RUFIO.
var gradientStops = [5][3]int{
	{255, 95, 130},  // R — hot pink
	{255, 140, 70},  // U — coral
	{255, 200, 50},  // F — amber
	{120, 220, 110}, // I — spring green
	{80, 200, 230},  // O — cyan
}

// Letter boundaries by character index in the wordmark rows.
var letterBounds = []int{0, 8, 17, 26, 31, 38}

const (
	esc   = "\x1b["
	reset = "\x1b[0m"
	bold  = "\x1b[1m"
	dim   = "\x1b[2m"
)

type colourMode int

const (
	modeNone colourMode = iota
	modeBasic
	mode256
	modeTrue
)

func detectMode() colourMode {
	if os.Getenv("NO_COLOR") != "" {
		return modeNone
	}
	if !isTTY(os.Stdout) {
		return modeNone
	}
	if t := os.Getenv("COLORTERM"); t == "truecolor" || t == "24bit" {
		return modeTrue
	}
	if strings.Contains(os.Getenv("TERM"), "256") {
		return mode256
	}
	if os.Getenv("TERM") != "" {
		return modeBasic
	}
	return modeNone
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func trueColour(r, g, b int) string {
	return fmt.Sprintf("%s38;2;%d;%d;%dm", esc, r, g, b)
}

func letterIndex(col int) int {
	for i := 0; i < len(letterBounds)-1; i++ {
		if col >= letterBounds[i] && col < letterBounds[i+1] {
			return i
		}
	}
	return 0
}

func colourGlyph(ch rune, col int, mode colourMode) string {
	if ch == ' ' || mode == modeNone {
		return string(ch)
	}
	li := letterIndex(col)
	if li >= len(gradientStops) {
		li = len(gradientStops) - 1
	}
	r, g, b := gradientStops[li][0], gradientStops[li][1], gradientStops[li][2]
	switch mode {
	case modeTrue:
		return fmt.Sprintf("%s%c%s", trueColour(r, g, b), ch, reset)
	case mode256:
		// Approximate to nearest 256-cube cell.
		r6, g6, b6 := r/51, g/51, b/51
		code := 16 + 36*r6 + 6*g6 + b6
		return fmt.Sprintf("%s38;5;%dm%c%s", esc, code, ch, reset)
	case modeBasic:
		// 8-colour fallback per letter — magenta, yellow, yellow, green, cyan.
		basicCodes := []int{35, 33, 33, 32, 36}
		return fmt.Sprintf("%s%dm%c%s", esc, basicCodes[li], ch, reset)
	}
	return string(ch)
}

func colourLine(line string, mode colourMode) string {
	var b strings.Builder
	for i, ch := range line {
		b.WriteString(colourGlyph(ch, i, mode))
	}
	return b.String()
}

// hairline draws the Hook-bandana easter-egg: half red, half amber.
func hairline(width int, mode colourMode) string {
	half := width / 2
	left := strings.Repeat("━", half)
	right := strings.Repeat("━", width-half)
	switch mode {
	case modeNone:
		return left + right
	case modeTrue:
		return fmt.Sprintf("%s%s%s%s%s", trueColour(220, 50, 50), left, trueColour(240, 200, 60), right, reset)
	default:
		return fmt.Sprintf("%s31m%s%s33m%s%s", esc, left, esc, right, reset)
	}
}

// normaliseVersion strips a single leading "v" prefix so the banner's
// own "v" prefix never doubles up. Release builds inject the version via
// `-X main.version=v1.0.6` (with the leading v); local builds use bare
// strings like "dev". Defensive strip here keeps the banner correct
// regardless of caller convention.
func normaliseVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// Print renders the full banner. Falls back to plain text on non-TTY /
// NO_COLOR / dumb terminals.
func Print(opts Options) {
	version := opts.Version
	if version == "" {
		version = "0.1.0"
	}
	version = normaliseVersion(version)
	tagline := opts.Tagline
	if tagline == "" {
		tagline = defaultTagline
	}

	mode := detectMode()
	if mode == modeNone {
		fmt.Fprintln(os.Stdout)
		for _, line := range wordmark {
			fmt.Fprintln(os.Stdout, line)
		}
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "  %s\n", tagline)
		if opts.ShowVersion {
			fmt.Fprintf(os.Stdout, "  v%s  ·  rufio.ai\n", version)
		}
		fmt.Fprintln(os.Stdout)
		return
	}

	fmt.Fprintln(os.Stdout)
	for _, line := range wordmark {
		fmt.Fprintln(os.Stdout, colourLine(line, mode))
	}
	fmt.Fprintln(os.Stdout, hairline(len(wordmark[0]), mode))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  %s%s%s\n", bold, tagline, reset)
	if opts.ShowVersion {
		fmt.Fprintf(os.Stdout, "  %sv%s  ·  rufio.ai%s\n", dim, version, reset)
	}
	fmt.Fprintln(os.Stdout)
}

// PrintCompact renders the one-line variant used by `rufio dev`.
func PrintCompact(opts Options) {
	version := opts.Version
	if version == "" {
		version = "0.1.0"
	}
	version = normaliseVersion(version)
	tagline := opts.Tagline
	if tagline == "" {
		tagline = defaultTagline
	}

	mode := detectMode()
	if mode == modeNone {
		fmt.Fprintf(os.Stdout, "\nrufio v%s · %s\n\n", version, tagline)
		return
	}

	letters := []rune{'r', 'u', 'f', 'i', 'o'}
	var mark strings.Builder
	for i, ch := range letters {
		r, g, b := gradientStops[i][0], gradientStops[i][1], gradientStops[i][2]
		switch mode {
		case modeTrue:
			fmt.Fprintf(&mark, "%s%s%c%s", bold, trueColour(r, g, b), ch, reset)
		default:
			fmt.Fprintf(&mark, "%s%c%s", bold, ch, reset)
		}
	}
	fmt.Fprintf(os.Stdout, "\n  %s  %sv%s · %s%s\n\n", mark.String(), dim, version, tagline, reset)
}
