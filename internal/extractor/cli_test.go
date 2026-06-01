package extractor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestEndToEndAllSignals runs every detector over a combined fixture corpus and
// asserts incidents appear across multiple signal types.
func TestEndToEndAllSignals(t *testing.T) {
	corpus := filepath.Join(t.TempDir(), "corpus")
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	srcs := []string{
		filepath.Join("testdata", "base"),
		filepath.Join("testdata", "tool_error"),
		filepath.Join("testdata", "user_correction"),
		filepath.Join("..", "..", "fixtures", "skeleton-first"),
	}
	for i, dir := range srcs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for j, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				t.Fatalf("read %s: %v", m, err)
			}
			dst := filepath.Join(corpus, filepath.Base(dir)+"-"+strconv.Itoa(i)+"-"+strconv.Itoa(j)+".jsonl")
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				t.Fatalf("write %s: %v", dst, err)
			}
		}
	}

	cfg := DefaultConfig()
	cfg.CorpusGlob = filepath.Join(corpus, "*.jsonl")
	cfg.OutDB = filepath.Join(t.TempDir(), "incidents.db")
	cfg.Signals = nil    // all
	cfg.MemoryLimit = "" // tiny fixture; avoid the production cap (see testConfig)
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := countBySignal(t, cfg.OutDB)
	for _, want := range []string{"inefficiency", "tool_error", "user_correction"} {
		if c[want] < 1 {
			t.Errorf("expected >=1 %s incident across the combined corpus, got %d", want, c[want])
		}
	}
}

// TestSignalsFilter runs only one detector and confirms no others fire.
func TestSignalsFilter(t *testing.T) {
	cfg := testConfig(t, "tool_error", "inefficiency") // ask for inefficiency only
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	for sig := range c {
		if sig != "inefficiency" {
			t.Errorf("filter leaked: expected only inefficiency signals, got %q in %v", sig, c)
		}
	}
}
