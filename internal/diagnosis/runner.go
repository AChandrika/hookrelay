package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"hookrelay/internal/store"
)

// Runner processes queued diagnoses in the background, one at a time. It runs
// in the worker process but never touches the delivery path: a slow or broken
// model can delay diagnoses, but never a webhook.
type Runner struct {
	Store    *store.Store
	LLM      *Client // nil means rules only
	Log      *slog.Logger
	Lease    time.Duration
	CacheTTL time.Duration
	Poll     time.Duration
}

func (r *Runner) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.Log.Error("diagnosis runner", "err", err)
		}
		if !worked {
			select {
			case <-ctx.Done():
			case <-time.After(r.Poll):
			}
		}
	}
}

// RunOnce claims and processes at most one diagnosis.
func (r *Runner) RunOnce(ctx context.Context) (bool, error) {
	if n, err := r.Store.ReapDiagnoses(ctx); err != nil {
		return false, err
	} else if n > 0 {
		r.Log.Warn("requeued stuck diagnoses", "count", n)
	}
	job, err := r.Store.ClaimDiagnosis(ctx, r.Lease)
	if err != nil || job == nil {
		return false, err
	}
	r.process(*job)
	return true, nil
}

func (r *Runner) process(job store.DiagnosisJob) {
	// Finish well before the lease expires, so the reaper never hands this job
	// to someone else while we're still working on it.
	ctx, cancel := context.WithTimeout(context.Background(), r.Lease-15*time.Second)
	defer cancel()
	log := r.Log.With("diagnosis_id", job.ID, "delivery_id", job.DeliveryID)

	dc, err := r.Store.LoadDiagnosisContext(ctx, job.DeliveryID, 5)
	if err != nil {
		r.fail(ctx, job, "could not load the delivery: "+err.Error())
		return
	}
	in := FromContext(dc)
	if len(in.Attempts) == 0 {
		r.fail(ctx, job, "this delivery has no attempts to analyze")
		return
	}
	fp := Fingerprint(dc.EndpointID, in)

	out := store.DiagnosisOutcome{Fingerprint: fp, PromptVersion: PromptVersion, Model: "rules"}
	var res Result

	if r.LLM != nil {
		out.Model = r.LLM.Model
		if cached, ok, err := r.Store.FindCachedDiagnosis(ctx, fp, r.LLM.Model, PromptVersion, time.Now().Add(-r.CacheTTL)); err != nil {
			log.Warn("cache lookup failed", "err", err)
		} else if ok {
			out.Result, out.Cached = cached, true
			r.complete(ctx, job, out, log)
			return
		}

		var usage Usage
		res, usage, err = r.LLM.Diagnose(ctx, in)
		out.DurationMS = int(usage.Duration.Milliseconds())
		out.PromptTokens, out.CompletionTokens = usage.PromptTokens, usage.CompletionTokens
		if err != nil {
			log.Warn("model diagnosis failed, using rules", "err", err)
			note := "The AI model was unavailable or returned invalid output, so this is the rule-based diagnosis. (" + err.Error() + ")"
			out.Note, out.Model = &note, "rules"
			res = Rules(in)
		} else {
			// Code-level checks: drop evidence that isn't in the input, and fall
			// back to the rules if the category contradicts the status code.
			var notes []string
			var usedRules bool
			res, notes, usedRules = Check(in, res)
			if usedRules {
				out.Model = "rules"
				log.Info("model answer failed the plausibility check", "notes", notes)
			}
			if len(notes) > 0 {
				note := strings.Join(notes, " ")
				out.Note = &note
			}
		}
	} else {
		res = Rules(in)
	}

	b, err := json.Marshal(res)
	if err != nil {
		r.fail(ctx, job, "could not encode result: "+err.Error())
		return
	}
	out.Result = b
	r.complete(ctx, job, out, log)
}

func (r *Runner) complete(ctx context.Context, job store.DiagnosisJob, out store.DiagnosisOutcome, log *slog.Logger) {
	err := r.Store.CompleteDiagnosis(ctx, job, out)
	switch {
	case errors.Is(err, store.ErrLeaseLost):
		log.Warn("diagnosis lease lost, discarding result")
	case err != nil:
		log.Error("could not save diagnosis", "err", err)
	default:
		log.Info("diagnosis done", "model", out.Model, "cached", out.Cached, "ms", out.DurationMS,
			"prompt_tokens", out.PromptTokens, "completion_tokens", out.CompletionTokens)
	}
}

func (r *Runner) fail(ctx context.Context, job store.DiagnosisJob, reason string) {
	if err := r.Store.FailDiagnosis(ctx, job, reason); err != nil {
		r.Log.Error("could not mark diagnosis failed", "err", err)
	}
}

// FromContext converts stored attempts into a prepared (redacted) Input.
func FromContext(dc store.DiagnosisContext) Input {
	in := Input{EndpointURL: dc.EndpointURL, EventType: dc.EventType}
	for _, a := range dc.Attempts {
		ai := AttemptInput{Number: a.AttemptNumber, StatusCode: a.ResponseStatus, DurationMS: a.DurationMS}
		if len(a.ResponseHeaders) > 0 {
			_ = json.Unmarshal(a.ResponseHeaders, &ai.Headers)
		}
		if a.ResponseBody != nil {
			ai.Body = *a.ResponseBody
		}
		if a.Error != nil {
			ai.Error = *a.Error
		}
		in.Attempts = append(in.Attempts, ai)
	}
	return Prepare(in)
}
