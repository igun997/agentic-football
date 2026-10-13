package room

import (
	"sort"
	"time"
)

// Bench measures how fast an AI provider's agent actually plays. Nothing here limits the agent:
// TPS is only counted, as a benchmark.
//
//   - measured by the server (trustworthy): calls/sec, turnaround (gap between a reply and the
//     agent's next call), reaction (observe -> action), authoring time (gap before a forge/create call)
//   - self-reported (unverified): provider, model, tokens/sec via report_usage (output tokens / generation time)
type Bench struct {
	Provider, Model string
	First, Last     time.Time
	Calls, Errors   int
	ByTool          map[string]int
	times           []time.Time // recent call timestamps for the rolling rate
	turn, react     []float64   // ms samples
	author          []float64
	lastReply       map[Role]time.Time
	lastObserve     time.Time
	TokensIn        int
	TokensOut       int
	GenMs           float64
	Reports         int
}

const maxSamples = 256

func newBench() *Bench {
	return &Bench{ByTool: map[string]int{}, lastReply: map[Role]time.Time{}}
}

func push(s []float64, v float64) []float64 {
	s = append(s, v)
	if len(s) > maxSamples {
		s = s[len(s)-maxSamples:]
	}
	return s
}

func pct(s []float64, p float64) float64 {
	if len(s) == 0 {
		return 0
	}
	c := append([]float64(nil), s...)
	sort.Float64s(c)
	return c[int(float64(len(c)-1)*p)]
}

func mean(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	t := 0.0
	for _, v := range s {
		t += v
	}
	return t / float64(len(s))
}

// record notes one finished tool call. start is when the call arrived, end when it was answered.
func (b *Bench) record(role Role, tool string, start, end time.Time, failed bool) {
	if b.First.IsZero() {
		b.First = start
	}
	b.Last = end
	b.Calls++
	b.ByTool[tool]++
	if failed {
		b.Errors++
	}
	b.times = append(b.times, start)
	if len(b.times) > 512 {
		b.times = b.times[len(b.times)-512:]
	}
	if prev, ok := b.lastReply[role]; ok {
		gap := float64(start.Sub(prev).Milliseconds())
		b.turn = push(b.turn, gap)
		if tool == "forge_skill" || tool == "create_skill" {
			b.author = push(b.author, gap)
		}
	}
	b.lastReply[role] = end
	switch tool {
	case "observe":
		b.lastObserve = end
	case "move", "kick", "dash", "use_skill", "switch_player":
		if !b.lastObserve.IsZero() {
			b.react = push(b.react, float64(start.Sub(b.lastObserve).Milliseconds()))
		}
	}
}

// BenchView is the JSON shape shown to spectators and agents.
type BenchView struct {
	Provider     string  `json:"provider,omitempty"`
	Model        string  `json:"model,omitempty"`
	CallsPerSec  float64 `json:"calls_per_sec"` // rolling 10s
	CallsAvg     float64 `json:"calls_avg"`     // since first call
	Calls        int     `json:"calls"`
	Errors       int     `json:"errors"`
	TurnP50Ms    float64 `json:"turn_p50_ms"`
	TurnP95Ms    float64 `json:"turn_p95_ms"`
	ReactionMs   float64 `json:"reaction_p50_ms"`
	AuthorAvgMs  float64 `json:"author_avg_ms"`
	TokensPerSec float64 `json:"tokens_per_sec"` // self-reported
	TokensOut    int     `json:"tokens_out"`
	TokensIn     int     `json:"tokens_in"`
	Reports      int     `json:"reports"`
}

func (b *Bench) view(now time.Time) BenchView {
	v := BenchView{Provider: b.Provider, Model: b.Model, Calls: b.Calls, Errors: b.Errors,
		TurnP50Ms: pct(b.turn, .5), TurnP95Ms: pct(b.turn, .95), ReactionMs: pct(b.react, .5), AuthorAvgMs: mean(b.author),
		TokensOut: b.TokensOut, TokensIn: b.TokensIn, Reports: b.Reports}
	n := 0
	for _, t := range b.times {
		if now.Sub(t) <= 10*time.Second {
			n++
		}
	}
	v.CallsPerSec = float64(n) / 10
	if !b.First.IsZero() {
		if el := now.Sub(b.First).Seconds(); el > 1 {
			v.CallsAvg = float64(b.Calls) / el
		}
	}
	if b.GenMs > 0 {
		v.TokensPerSec = float64(b.TokensOut) / (b.GenMs / 1000)
	}
	return v
}
