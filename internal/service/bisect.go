package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type RevisionEvaluator func(context.Context, string) (types.RevisionEvaluation, error)

type BisectService struct{}

func NewBisectService() *BisectService { return &BisectService{} }

// Search narrows by midpoint, then validates the full ancestry path before it
// makes an unconditional first-bad claim.
func (s *BisectService) Search(ctx context.Context, revisions []string, assumeMonotonic bool, evaluate RevisionEvaluator) (types.BisectResult, error) {
	if len(revisions) < 2 {
		return types.BisectResult{}, types.NewError(types.ErrValidation, "bisect requires at least good and bad revisions", "provide an ordered ancestry path", nil)
	}
	cache := map[int]types.RevisionEvaluation{}
	run := func(index int) (types.RevisionEvaluation, error) {
		if result, ok := cache[index]; ok {
			return result, nil
		}
		result, err := evaluate(ctx, revisions[index])
		if err != nil {
			return result, err
		}
		if result.Revision == "" {
			result.Revision = revisions[index]
		}
		if result.Revision != revisions[index] {
			return result, types.NewError(types.ErrEvaluator, "revision evaluator returned evidence for another revision", "return the exact requested revision", nil)
		}
		cache[index] = result
		return result, nil
	}
	good, err := run(0)
	if err != nil {
		return types.BisectResult{}, err
	}
	bad, err := run(len(revisions) - 1)
	if err != nil {
		return types.BisectResult{}, err
	}
	if good.Outcome != types.RevisionGood || bad.Outcome != types.RevisionBad {
		return inconclusive(cache, "the supplied endpoints are not confirmed good and bad"), nil
	}
	low, high := 0, len(revisions)-1
	for high-low > 1 {
		middle := low + (high-low)/2
		result, err := run(middle)
		if err != nil {
			return types.BisectResult{}, err
		}
		switch result.Outcome {
		case types.RevisionGood:
			low = middle
		case types.RevisionBad:
			high = middle
		default:
			return inconclusive(cache, fmt.Sprintf("revision %s could not be classified", revisions[middle])), nil
		}
	}
	if assumeMonotonic {
		return types.BisectResult{Outcome: "conditional_on_monotonicity", FirstBad: revisions[high], Evaluations: orderedEvaluations(cache), ConditionalOn: "monotonic_history", Message: "history outside midpoint evaluations was not checked"}, nil
	}
	for index := range revisions {
		result, err := run(index)
		if err != nil {
			return types.BisectResult{}, err
		}
		if result.Outcome == types.RevisionInconclusive {
			return inconclusive(cache, fmt.Sprintf("revision %s could not be classified", revisions[index])), nil
		}
	}
	seenBad, firstBad, nonMonotonic := false, "", false
	for index := range revisions {
		switch cache[index].Outcome {
		case types.RevisionBad:
			if !seenBad {
				firstBad = revisions[index]
			}
			seenBad = true
		case types.RevisionGood:
			if seenBad {
				nonMonotonic = true
			}
		}
	}
	if nonMonotonic {
		return types.BisectResult{Outcome: "non_monotonic", Evaluations: orderedEvaluations(cache), NonMonotonic: true, Message: "a good revision follows a bad revision on the selected ancestry path"}, nil
	}
	return types.BisectResult{Outcome: "first_bad", FirstBad: firstBad, Evaluations: orderedEvaluations(cache)}, nil
}

func ClassifyRevision(revision string, passes, trials int, passThreshold float64, evidence []string) types.RevisionEvaluation {
	result := types.RevisionEvaluation{Revision: revision, Passes: passes, Trials: trials, Evidence: evidence, Outcome: types.RevisionInconclusive}
	if trials <= 0 || passes < 0 || passes > trials || passThreshold < 0 || passThreshold > 1 {
		return result
	}
	result.Confidence = float64(passes) / float64(trials)
	if result.Confidence >= passThreshold {
		result.Outcome = types.RevisionGood
	} else {
		result.Outcome = types.RevisionBad
	}
	return result
}

func inconclusive(cache map[int]types.RevisionEvaluation, message string) types.BisectResult {
	return types.BisectResult{Outcome: "inconclusive", Evaluations: orderedEvaluations(cache), Message: message}
}
func orderedEvaluations(cache map[int]types.RevisionEvaluation) []types.RevisionEvaluation {
	keys := make([]int, 0, len(cache))
	for index := range cache {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	result := make([]types.RevisionEvaluation, 0, len(keys))
	for _, index := range keys {
		result = append(result, cache[index])
	}
	return result
}
