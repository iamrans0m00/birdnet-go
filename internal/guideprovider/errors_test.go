package guideprovider

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestErrGuideCacheNotAvailable_IsWithWrapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"direct", ErrGuideCacheNotAvailable, true},
		{"fmt %w wrap", fmt.Errorf("ctx: %w", ErrGuideCacheNotAvailable), true},
		{"custom errors.New wrap", errors.New(ErrGuideCacheNotAvailable).Component("test").Build(), true},
		{"double wrap", fmt.Errorf("outer: %w", errors.New(ErrGuideCacheNotAvailable).Build()), true},
		{"stdlib errors.Is direct", ErrGuideCacheNotAvailable, true},
		{"unrelated error", errors.Newf("different error").Build(), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errors.Is(tc.err, ErrGuideCacheNotAvailable); got != tc.want {
				t.Errorf("errors.Is = %v, want %v", got, tc.want)
			}
			if got := stderrors.Is(tc.err, ErrGuideCacheNotAvailable); got != tc.want {
				t.Errorf("stderrors.Is = %v, want %v", got, tc.want)
			}
		})
	}
}
