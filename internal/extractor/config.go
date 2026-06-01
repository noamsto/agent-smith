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

	// retry / correction / disagreement windows
	RetryWindowTurns   int
	CorrectionLookback int
	DisagreeWindow     int

	// regexes (RE2; no single quotes — they break SQL string literals)
	CorrectionRegex string
	DisagreeRegex   string

	// artifact resolution
	GlobalClaudeMd string // path to global CLAUDE.md
	AgentsDir      string // dir holding subagent .md files
}

// AllSignals is the canonical ordered detector list.
var AllSignals = []string{
	"inefficiency",
	"tool_error",
	"user_correction",
	"orchestrator_disagreement",
}

// signalFile maps a signal name to its SQL template filename.
var signalFile = map[string]string{
	"inefficiency":              "10_inefficiency.sql.tmpl",
	"tool_error":                "20_tool_error.sql.tmpl",
	"user_correction":           "30_user_correction.sql.tmpl",
	"orchestrator_disagreement": "40_orchestrator_disagreement.sql.tmpl",
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
		DisagreeWindow:     4,
		CorrectionRegex:    `(\bno\b|\bdon.?t\b|\bactually\b|\brevert\b|that.?s wrong|\bwrong\b|\bundo\b|\bnope\b|incorrect|\bstop\b)`,
		// Overrule/redo phrasings only — deliberately omits bare "wrong"/"incorrect"/"the
		// subagent", which match agreement ("the subagent found where X is wrong") and explode
		// false positives. Tuned against real Agent-result reactions in the corpus.
		DisagreeRegex:  `(\bdisagree|not what i asked|that.?s not right|that.?s not correct|that.?s wrong|that.?s incorrect|let me redo|let me just redo|i.?ll redo|i.?ll do this myself|i.?ll do it myself|redo this myself|the subagent is wrong|the subagent was wrong|subagent got it wrong|subagent is incorrect|ignore the subagent|\boverrule)`,
		GlobalClaudeMd: filepath.Join(home, ".claude", "CLAUDE.md"),
		AgentsDir:      filepath.Join(home, "nix-config", "home", "ai", "claude-code", "agents"),
	}
}
