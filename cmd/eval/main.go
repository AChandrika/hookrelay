// eval scores the diagnosis feature against scenarios with known answers.
//
//	eval.exe                                   # rules, the model, and the model + checks
//	eval.exe -rules-only                       # fast, no model (used in CI)
//	eval.exe -scenarios evals/holdout.json     # held-out set, not used to design the checks
//	eval.exe -model qwen3:4b                   # compare another model
//
// Three systems are scored side by side. "rules" is the baseline. The raw
// model shows what the LLM does on its own. "+checks" is what production
// shows users: the model's answer after the plausibility check and evidence
// filter. Checks reuse the same model answer, so they cost no extra calls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"hookrelay/internal/diagnosis"
)

type Scenario struct {
	Name       string                   `json:"name"`
	Note       string                   `json:"note,omitempty"`
	URL        string                   `json:"endpoint_url"`
	EventType  string                   `json:"event_type"`
	Attempts   []diagnosis.AttemptInput `json:"attempts"` // newest first
	Expected   string                   `json:"expected_category"`
	AlsoAccept []string                 `json:"also_accept,omitempty"`     // genuinely ambiguous cases
	Keywords   []string                 `json:"expect_keywords,omitempty"` // any one must appear in the explanation
}

type Outcome struct {
	Scenario   string            `json:"scenario"`
	System     string            `json:"system"`
	Result     *diagnosis.Result `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
	Correct    bool              `json:"correct"`
	KeywordHit *bool             `json:"keyword_hit,omitempty"`
	Exact      int               `json:"evidence_exact"`
	Loose      int               `json:"evidence_reformatted"`
	Missing    int               `json:"evidence_not_found"`
	FellBack   bool              `json:"fell_back_to_rules,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
	Millis     int64             `json:"ms"`
	Tokens     int               `json:"tokens"`
}

func main() {
	file := flag.String("scenarios", "evals/scenarios.json", "scenario file")
	model := flag.String("model", envOr("OLLAMA_MODEL", "qwen3:4b"), "Ollama model")
	url := flag.String("ollama", envOr("OLLAMA_URL", "http://127.0.0.1:11434"), "Ollama URL")
	rulesOnly := flag.Bool("rules-only", false, "skip the model; score the rules baseline only")
	minAcc := flag.Float64("min-accuracy", 0, "exit 1 if the production system's accuracy is below this (0-1)")
	out := flag.String("out", "", "write per-scenario results as JSON to this file")
	flag.Parse()

	raw, err := os.ReadFile(*file)
	check(err)
	var scenarios []Scenario
	check(json.Unmarshal(raw, &scenarios))

	var client *diagnosis.Client
	if !*rulesOnly {
		client = diagnosis.NewClient(*url, *model)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		check(client.Check(ctx))
		cancel()
	}
	checked := *model + "+checks"

	var results []Outcome
	for i, sc := range scenarios {
		in := diagnosis.Prepare(diagnosis.Input{EndpointURL: sc.URL, EventType: sc.EventType, Attempts: sc.Attempts})
		rendered := diagnosis.RenderInput(in)

		rr := diagnosis.Rules(in)
		results = append(results, score(sc, "rules", &rr, nil, rendered, 0, 0))

		if client == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "[%d/%d] %s ... ", i+1, len(scenarios), sc.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		start := time.Now()
		res, usage, err := client.Diagnose(ctx, in)
		elapsed := time.Since(start)
		cancel()
		tokens := usage.PromptTokens + usage.CompletionTokens

		o := score(sc, *model, &res, err, rendered, elapsed, tokens)
		results = append(results, o)

		var c Outcome
		if err != nil {
			c = score(sc, checked, nil, err, rendered, elapsed, tokens)
		} else {
			final, notes, fellBack := diagnosis.Check(in, res)
			c = score(sc, checked, &final, nil, rendered, elapsed, tokens)
			c.FellBack, c.Notes = fellBack, notes
		}
		results = append(results, c)
		fmt.Fprintf(os.Stderr, "model %s, with checks %s (%s)\n", mark(o.Correct), mark(c.Correct), elapsed.Round(100*time.Millisecond))
	}

	systems := []string{"rules"}
	if client != nil {
		systems = append(systems, *model, checked)
	}
	fmt.Printf("\nScenarios: %s\n\n", *file)
	printTable(scenarios, results, systems)
	accuracy := printSummary(results, systems)
	printFallbacks(results, checked)

	if *out != "" {
		b, _ := json.MarshalIndent(results, "", "  ")
		check(os.WriteFile(*out, b, 0o644))
	}
	scored := systems[len(systems)-1]
	if accuracy[scored] < *minAcc {
		fmt.Printf("\nFAIL: %s accuracy %.0f%% is below the minimum %.0f%%\n", scored, accuracy[scored]*100, *minAcc*100)
		os.Exit(1)
	}
}

func score(sc Scenario, system string, r *diagnosis.Result, err error, rendered string, d time.Duration, tokens int) Outcome {
	o := Outcome{Scenario: sc.Name, System: system, Millis: d.Milliseconds(), Tokens: tokens}
	if err != nil {
		o.Error = err.Error()
		return o
	}
	o.Result = r
	o.Correct = r.Category == sc.Expected || slices.Contains(sc.AlsoAccept, r.Category)
	if len(sc.Keywords) > 0 {
		text := strings.ToLower(r.LikelyCause + " " + r.SuggestedFix + " " + r.Summary)
		hit := slices.ContainsFunc(sc.Keywords, func(k string) bool { return strings.Contains(text, strings.ToLower(k)) })
		o.KeywordHit = &hit
	}
	for _, e := range r.Evidence {
		switch diagnosis.EvidenceMatch(e, rendered) {
		case "exact":
			o.Exact++
		case "loose":
			o.Loose++
		default:
			o.Missing++
		}
	}
	return o
}

func printTable(scenarios []Scenario, results []Outcome, systems []string) {
	byKey := map[string]Outcome{}
	for _, o := range results {
		byKey[o.Scenario+"|"+o.System] = o
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	header := "SCENARIO\tEXPECTED"
	for _, s := range systems {
		header += "\t" + strings.ToUpper(s)
	}
	fmt.Fprintln(tw, header)
	for _, sc := range scenarios {
		line := sc.Name + "\t" + sc.Expected
		for _, s := range systems {
			o := byKey[sc.Name+"|"+s]
			switch {
			case o.Error != "":
				line += "\tERROR"
			case o.Correct:
				line += "\tok"
			default:
				line += "\tMISS " + o.Result.Category
			}
		}
		fmt.Fprintln(tw, line)
	}
	tw.Flush()
}

func printSummary(results []Outcome, systems []string) map[string]float64 {
	acc := map[string]float64{}
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SYSTEM\tACCURACY\tKEY IDEA MENTIONED\tEVIDENCE EXACT / REFORMATTED / NOT FOUND\tERRORS\tAVG TIME\tAVG TOKENS")
	for _, s := range systems {
		var n, correct, kwN, kwHit, exact, loose, missing, errs, tokens int
		var ms int64
		for _, o := range results {
			if o.System != s {
				continue
			}
			n++
			ms += o.Millis
			tokens += o.Tokens
			if o.Error != "" {
				errs++
				continue
			}
			if o.Correct {
				correct++
			}
			if o.KeywordHit != nil {
				kwN++
				if *o.KeywordHit {
					kwHit++
				}
			}
			exact += o.Exact
			loose += o.Loose
			missing += o.Missing
		}
		acc[s] = ratio(correct, n)
		avg := time.Duration(ms/int64(max(n, 1))) * time.Millisecond
		fmt.Fprintf(tw, "%s\t%d/%d (%.0f%%)\t%d/%d\t%d / %d / %d\t%d\t%s\t%d\n", s, correct, n, acc[s]*100,
			kwHit, kwN, exact, loose, missing, errs, avg.Round(time.Millisecond), tokens/max(n, 1))
	}
	tw.Flush()
	return acc
}

func printFallbacks(results []Outcome, checked string) {
	var names []string
	for _, o := range results {
		if o.System == checked && o.FellBack {
			names = append(names, o.Scenario)
		}
	}
	if len(names) > 0 {
		fmt.Printf("\nThe plausibility check replaced the model's answer with the rules in %d scenario(s): %s\n",
			len(names), strings.Join(names, ", "))
	}
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func mark(ok bool) string {
	if ok {
		return "ok"
	}
	return "MISS"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval:", err)
		os.Exit(1)
	}
}
