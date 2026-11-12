// Command voice-eval measures voice logging (D36).
//
//	make voice-eval                      # dry run: case count and estimated cost, no API call
//	make voice-eval ARGS=-yes            # run the phrase set against the real model (costs money)
//	make voice-eval ARGS="-stt eval/voice/stt.jsonl"   # score captured speech-to-text; no API call
//
// Extraction and speech-to-text are reported separately. Only cases with source "real" may be
// quoted; drafted cases check the pipeline, not the product.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/sudhanshubhushan4535/calmfit/internal/config"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice"
	"github.com/sudhanshubhushan4535/calmfit/internal/voice/eval"
)

// Assumed tokens per call for the dry-run estimate only; a real run reports measured usage.
const estInTokens, estOutTokens = 1500, 150

func main() {
	var (
		phrases  = flag.String("phrases", "eval/voice/phrases.jsonl", "phrase set (JSONL)")
		sttFile  = flag.String("stt", "", "captured speech-to-text records (JSONL); scores them without calling the API")
		yes      = flag.Bool("yes", false, "really call the API (costs money)")
		limit    = flag.Int("limit", 0, "run only the first N cases (0 = all)")
		realOnly = flag.Bool("real-only", false, "run only cases with source \"real\"")
		cfgPath  = flag.String("config", "config/app.json", "app config (for voice.model)")
		inPrice  = flag.Float64("usd-in", 0.10, "USD per million input tokens")
		outPrice = flag.Float64("usd-out", 0.50, "USD per million output tokens")
		inrRate  = flag.Float64("inr-per-usd", 0, "INR per USD for the ₹ line (0 = USD only)")
	)
	flag.Parse()
	if err := run(*phrases, *sttFile, *yes, *limit, *realOnly, *cfgPath, eval.Prices{InPerMTok: *inPrice, OutPerMTok: *outPrice, INRPerUSD: *inrRate}); err != nil {
		log.Fatal(err)
	}
}

func run(phrases, sttFile string, yes bool, limit int, realOnly bool, cfgPath string, pr eval.Prices) error {
	if sttFile != "" {
		recs, err := eval.LoadSTT(sttFile)
		if err != nil {
			return err
		}
		r := eval.ScoreSTT(recs)
		fmt.Printf("Speech-to-text (%s, %d records)\n", sttFile, r.Records)
		fmt.Printf("  exact match (normalised)  %5.1f%%\n", 100*r.ExactMatch)
		fmt.Printf("  word error rate           %5.1f%%\n", 100*r.WER)
		return nil
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	cat, err := eval.LoadCatalog("seed/exercises.json", "seed/templates.json")
	if err != nil {
		return err
	}
	cases, err := eval.LoadCases(phrases, cat)
	if err != nil {
		return err
	}
	if realOnly {
		var real []eval.Case
		for _, c := range cases {
			if c.Source == "real" {
				real = append(real, c)
			}
		}
		cases = real
	}
	if limit > 0 && limit < len(cases) {
		cases = cases[:limit]
	}
	if len(cases) == 0 {
		return fmt.Errorf("no cases to run")
	}

	est := float64(len(cases)) * (estInTokens*pr.InPerMTok + estOutTokens*pr.OutPerMTok) / 1e6
	fmt.Printf("%d cases from %s on %s; estimated cost ≈ $%.4f (assumes ~%d in / %d out tokens per call)\n",
		len(cases), phrases, cfg.Voice.Model, est, estInTokens, estOutTokens)
	if !yes {
		fmt.Println("Dry run: nothing was sent. Re-run with -yes to call the API.")
		return nil
	}
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is not set (see .env.example)")
	}

	parser := voice.NewAPIParser(key, cfg.Voice.Model)
	outs := eval.Run(context.Background(), parser, cases, cat, time.Now)
	printReport(outs, eval.Summarise(outs, pr), pr)
	return nil
}

func printReport(outs []eval.Outcome, r eval.Report, pr eval.Prices) {
	fmt.Printf("\nExtraction (%d cases: %d real, %d drafted)\n", r.Cases, r.Real, r.Drafted)
	if r.Real == 0 {
		fmt.Println("  NOTE: drafted cases only. These numbers check the pipeline; don't quote them (D36).")
	}
	fmt.Printf("  exact, all cases            %5.1f%%\n", 100*r.Exact)
	fmt.Printf("  set logs (%d): exercise      %5.1f%%\n", r.Accept, 100*r.Exercise)
	fmt.Printf("                reps          %5.1f%%\n", 100*r.Reps)
	fmt.Printf("                weights       %5.1f%%\n", 100*r.Weights)
	fmt.Printf("  rejections (%d) right        %5.1f%%\n", r.Reject, 100*r.RejectRight)
	fmt.Printf("  provider errors             %d\n", r.ProviderErrors)
	fmt.Printf("Latency p50 %v, p95 %v\n", r.P50.Round(time.Millisecond), r.P95.Round(time.Millisecond))
	fmt.Printf("Tokens %d in / %d out; cost $%.4f total, $%.6f per call", r.InputTokens, r.OutputTokens, r.Total, r.CostPerCallUSD)
	if pr.INRPerUSD > 0 {
		fmt.Printf(" (₹%.4f per call)", r.CostPerCallINR)
	}
	fmt.Println()

	var failed []string
	for _, o := range outs {
		if o.Exact {
			continue
		}
		got := fmt.Sprintf("%s %v", o.Got.ExerciseID, o.Got.Sets)
		if o.Err != nil {
			got = o.Err.Error()
		}
		want := "reject"
		if o.Case.Expect != nil {
			want = fmt.Sprintf("%s %v", o.Case.Expect.ExerciseID, o.Case.Expect.Sets)
		}
		failed = append(failed, fmt.Sprintf("  %-8s %q\n           want %s\n           got  %s", o.Case.ID, o.Case.Transcript, want, got))
	}
	if len(failed) > 0 {
		fmt.Printf("\nWrong (%d):\n%s\n", len(failed), strings.Join(failed, "\n"))
	}
}
