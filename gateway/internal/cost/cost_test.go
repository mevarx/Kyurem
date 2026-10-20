package cost

import (
	"math"
	"testing"
)

// epsilon is the absolute tolerance used to compare floating point sums.
const epsilon = 1e-9

// almostEqual compares two costs within epsilon.
func almostEqual(a, b float64) bool { return math.Abs(a-b) <= epsilon }

func TestTableCost(t *testing.T) {
	table := NewTable([]Price{
		{Model: "gpt-4o", InputPer1KUSD: 0.002, OutputPer1KUSD: 0.008},
		{Model: "claude-sonnet", InputPer1KUSD: 0.003, OutputPer1KUSD: 0.015},
		{Model: "free", InputPer1KUSD: 0, OutputPer1KUSD: 0},
	})

	cases := []struct {
		name    string
		model   string
		in      int64
		out     int64
		wantUSD float64
		wantOK  bool
	}{
		{name: "exact match", model: "gpt-4o", in: 1000, out: 1000, wantUSD: 0.010, wantOK: true},
		{name: "exact match other model", model: "claude-sonnet", in: 1000, out: 1000, wantUSD: 0.018, wantOK: true},
		{name: "case-insensitive match", model: "GPT-4O", in: 1000, out: 0, wantUSD: 0.002, wantOK: true},
		{name: "case-insensitive match mixed case", model: "Claude-Sonnet", in: 0, out: 1000, wantUSD: 0.015, wantOK: true},
		{name: "unknown model", model: "llama-3", in: 1000, out: 1000, wantUSD: 0, wantOK: false},
		{name: "empty model", model: "", in: 1000, out: 1000, wantUSD: 0, wantOK: false},
		{name: "zero tokens", model: "gpt-4o", in: 0, out: 0, wantUSD: 0, wantOK: true},
		{name: "rounding", model: "gpt-4o", in: 1500, out: 0, wantUSD: 0.003, wantOK: true},
		{name: "negative tokens clamped", model: "gpt-4o", in: -1000, out: -1000, wantUSD: 0, wantOK: true},
		{name: "input and output combined", model: "claude-sonnet", in: 1000, out: 2000, wantUSD: 0.033, wantOK: true},
		{name: "free model", model: "free", in: 100000, out: 100000, wantUSD: 0, wantOK: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := table.Cost(tc.model, tc.in, tc.out)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (cost %v)", ok, tc.wantOK, got)
			}
			if !almostEqual(got, tc.wantUSD) {
				t.Errorf("cost = %v, want %v", got, tc.wantUSD)
			}
		})
	}
}

func TestNewTableLaterEntryWins(t *testing.T) {
	table := NewTable([]Price{
		{Model: "gpt-4o", InputPer1KUSD: 0.001, OutputPer1KUSD: 0.002},
		{Model: "gpt-4o", InputPer1KUSD: 0.005, OutputPer1KUSD: 0.010},
	})

	got, ok := table.Cost("gpt-4o", 1000, 1000)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !almostEqual(got, 0.015) {
		t.Errorf("cost = %v, want 0.015 (the later entry must win)", got)
	}
}

func TestNewTableEmpty(t *testing.T) {
	table := NewTable(nil)
	if _, ok := table.Cost("gpt-4o", 1000, 1000); ok {
		t.Error("an empty table must not price anything")
	}

	// The zero value is an empty table too.
	var zero Table
	if _, ok := zero.Cost("gpt-4o", 1000, 1000); ok {
		t.Error("the zero table must not price anything")
	}
}

func TestNewTableSkipsEntriesWithoutModel(t *testing.T) {
	table := NewTable([]Price{{InputPer1KUSD: 1, OutputPer1KUSD: 1}})
	if _, ok := table.Cost("", 1000, 1000); ok {
		t.Error("an entry without a model must not price anything")
	}
}
