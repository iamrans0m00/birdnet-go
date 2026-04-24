package guideprovider

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestErrGuideCacheNotAvailable_IsWithWrapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"direct", ErrGuideCacheNotAvailable, true},
		{"fmt %w wrap", fmt.Errorf("ctx: %w", ErrGuideCacheNotAvailable), true},
		{"custom errors.New wrap", errors.New(ErrGuideCacheNotAvailable).Component("test").Build(), true},
		{"double wrap", fmt.Errorf("outer: %w", errors.New(ErrGuideCacheNotAvailable).Build()), true},
		{"unrelated error", errors.Newf("different error").Build(), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, errors.Is(tc.err, ErrGuideCacheNotAvailable), "internal errors.Is")
			assert.Equal(t, tc.want, stderrors.Is(tc.err, ErrGuideCacheNotAvailable), "stdlib errors.Is")
		})
	}
}
