package extractor

import (
	"os"
	"path/filepath"
)

// Config drives both the Go orchestration and the SQL templates.
type Config struct {
	CorpusGlob string   // glob of .jsonl files to mine
	OutDB      string   // output DuckDB file
	Since      string   // optional ISO8601 lower bound on record timestamp; "" = all
	Signals    []string // which detectors to run; empty = all

	// DuckDB runtime settings
	MemoryLimit string // DuckDB memory_limit pragma (e.g. "30GB"); "" = duckdb default
	Threads     int    // DuckDB threads pragma; 0 = duckdb default

	// Window (transcript slice stored per incident)
	WindowBefore int
	WindowAfter  int
	ExcerptChars int

	// inefficiency thresholds (line counts)
	LargeFileLines int
	MediumLines    int
	HighLines      int

	// retry / correction windows
	RetryWindowTurns   int
	CorrectionLookback int

	// regexes (RE2; no single quotes — they break SQL string literals)
	CorrectionRegex string

	// artifact resolution
	GlobalClaudeMd string // path to global CLAUDE.md
}

// AllSignals is the canonical ordered detector list.
var AllSignals = []string{
	"inefficiency",
	"tool_error",
	"user_correction",
}

// signalFile maps a signal name to its SQL template filename.
var signalFile = map[string]string{
	"inefficiency":    "10_inefficiency.sql.tmpl",
	"tool_error":      "20_tool_error.sql.tmpl",
	"user_correction": "30_user_correction.sql.tmpl",
}

// DefaultConfig returns production defaults, resolving paths under $HOME.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir() // error ignored: home becomes "" on failure
	return Config{
		CorpusGlob:         filepath.Join(home, ".claude", "projects", "**", "*.jsonl"),
		OutDB:              "incidents.db",
		Since:              "",
		Signals:            nil,
		MemoryLimit:        "8GB",
		Threads:            0,
		WindowBefore:       3,
		WindowAfter:        4,
		ExcerptChars:       300,
		LargeFileLines:     300,
		MediumLines:        500,
		HighLines:          1000,
		RetryWindowTurns:   5,
		CorrectionLookback: 2,
		CorrectionRegex:    `(\bno\b|\bdon.?t\b|\bactually\b|\brevert\b|that.?s wrong|\bwrong\b|\bundo\b|\bnope\b|incorrect|\bstop\b)`,
		GlobalClaudeMd:     filepath.Join(home, ".claude", "CLAUDE.md"),
	}
}
