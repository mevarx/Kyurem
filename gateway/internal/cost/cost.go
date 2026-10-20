// Package cost implements the price model of the Kyurem gateway
// (PRD §4.3, CO-2).
//
// The package turns the token usage an agent reported into a price. The
// aggregation of the per-span costs lives in the storage query layer, which
// is the only place that sums spans; this package owns the prices.
//
// Two rules shape the API:
//
//   - Unknown is never zero. When the upstream does not report token usage,
//     or the reported model has no price entry, the cost is UNKNOWN, not
//     $0. The gateway therefore writes no cost attribute at all instead of
//     writing a zero.
//   - A table is immutable. It is built once and shared by every goroutine
//     without locking.
package cost

import "strings"

// Price is one entry of the price table: the price per 1000 tokens for a
// model (or model alias) reported by an agent.
type Price struct {
	Model          string
	InputPer1KUSD  float64
	OutputPer1KUSD float64
}

// Table is an immutable lookup of prices by model.
//
// A table is built once with NewTable and never written to afterwards, so
// one table can be shared by every goroutine without locking. The zero
// Table is an empty table: every lookup misses.
type Table struct {
	// exact holds the prices keyed by the model as written.
	exact map[string]Price
	// folded holds the same prices keyed by the lower-cased model, used
	// for the case-insensitive fallback lookup.
	folded map[string]Price
}

// NewTable builds a price table. Later entries for the same model win.
//
// Entries without a model are skipped: an unnamed entry could only ever be
// matched by a lookup of the empty model, and an empty model means "the
// agent did not report one", which must stay unknown.
func NewTable(prices []Price) Table {
	t := Table{
		exact:  make(map[string]Price, len(prices)),
		folded: make(map[string]Price, len(prices)),
	}
	for _, p := range prices {
		if p.Model == "" {
			continue
		}
		t.exact[p.Model] = p
		t.folded[strings.ToLower(p.Model)] = p
	}
	return t
}

// Cost returns the cost in USD for the given token counts and whether a
// price was found. ok is false when the model has no price entry, which
// means the cost is unknown, not free.
//
// The lookup is exact first and case-insensitive second, so "GPT-4o" and
// "gpt-4o" resolve to the same entry. Negative token counts are clamped to
// zero: usage is a count, never a negative number.
func (t Table) Cost(model string, inputTokens, outputTokens int64) (float64, bool) {
	p, ok := t.lookup(model)
	if !ok {
		return 0, false
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	return float64(inputTokens)/1000*p.InputPer1KUSD +
		float64(outputTokens)/1000*p.OutputPer1KUSD, true
}

// lookup resolves a model name against the table.
func (t Table) lookup(model string) (Price, bool) {
	if model == "" {
		return Price{}, false
	}
	if p, ok := t.exact[model]; ok {
		return p, true
	}
	p, ok := t.folded[strings.ToLower(model)]
	return p, ok
}
