package actionmods

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestUnknownFailuresRequireReviewAndRedactDetails(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("secret response payload")} {
		result := ResultFromError(err)
		if result.Retryable || !result.OutcomeUncertain || result.Error == err.Error() {
			t.Fatalf("unclassified error must require review: %+v", result)
		}
	}
}

func TestConfigIntRejectsFractionalAndOverflowValues(t *testing.T) {
	for _, value := range []float64{1.5, math.Inf(1), math.NaN(), math.MaxFloat64} {
		if got := configInt(map[string]any{"duration": value}, "duration"); got != 0 {
			t.Fatalf("accepted %v as %d", value, got)
		}
	}
	if got := configInt(map[string]any{"duration": float64(60)}, "duration"); got != 60 {
		t.Fatalf("valid duration: %d", got)
	}
}
