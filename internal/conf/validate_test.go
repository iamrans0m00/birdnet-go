package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestValidateSoundLevelSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings SoundLevelSettings
		wantErr  bool
		errType  string
	}{
		{
			name: "disabled sound level - should pass regardless of interval",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: 1,
			},
			wantErr: false,
		},
		{
			name: "enabled with interval less than 5 seconds - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 4,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with interval exactly 5 seconds - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 5,
			},
			wantErr: false,
		},
		{
			name: "enabled with interval greater than 5 seconds - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 10,
			},
			wantErr: false,
		},
		{
			name: "enabled with zero interval - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 0,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with negative interval - should fail",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: -5,
			},
			wantErr: true,
			errType: "sound-level-interval",
		},
		{
			name: "enabled with very high interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  true,
				Interval: 3600,
			},
			wantErr: false,
		},
		{
			name: "disabled with zero interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: 0,
			},
			wantErr: false,
		},
		{
			name: "disabled with negative interval - should pass",
			settings: SoundLevelSettings{
				Enabled:  false,
				Interval: -10,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSoundLevelSettings(&tt.settings)

			if tt.wantErr {
				enhanced := requireEnhancedError(t, err)

				// Verify validation type context
				ctx, exists := enhanced.Context["validation_type"]
				assert.True(t, exists, "expected validation_type context to be set")
				assert.Equal(t, tt.errType, ctx)

				// Verify error category
				assert.Equal(t, errors.CategoryValidation, enhanced.Category)

				// Verify interval context for interval errors
				if tt.errType == "sound-level-interval" {
					assert.Contains(t, enhanced.Context, "interval")
					assert.Contains(t, enhanced.Context, "minimum_interval")
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateSoundLevelSettingsEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		interval int
		enabled  bool
		wantErr  bool
	}{
		{"boundary: 4 seconds enabled", 4, true, true},
		{"boundary: 5 seconds enabled", 5, true, false},
		{"boundary: 6 seconds enabled", 6, true, false},
		{"boundary: 4 seconds disabled", 4, false, false},
		{"boundary: 5 seconds disabled", 5, false, false},
		{"boundary: 6 seconds disabled", 6, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &SoundLevelSettings{
				Enabled:  tt.enabled,
				Interval: tt.interval,
			}

			err := validateSoundLevelSettings(settings)
			if tt.wantErr {
				assert.Error(t, err, "interval %d with enabled=%v should fail", tt.interval, tt.enabled)
			} else {
				assert.NoError(t, err, "interval %d with enabled=%v should pass", tt.interval, tt.enabled)
			}
		})
	}
}

func TestValidateSoundLevelSettingsErrorMessage(t *testing.T) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 3,
	}

	err := validateSoundLevelSettings(settings)
	require.Error(t, err, "expected error for interval < 5 seconds")

	expectedMsg := "sound level interval must be at least 5 seconds to avoid excessive CPU usage, got 3"
	assert.Equal(t, expectedMsg, err.Error())
}

func BenchmarkValidateSoundLevelSettings(b *testing.B) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 10,
	}

	for b.Loop() {
		_ = validateSoundLevelSettings(settings)
	}
}

func BenchmarkValidateSoundLevelSettingsWithError(b *testing.B) {
	settings := &SoundLevelSettings{
		Enabled:  true,
		Interval: 2,
	}

	for b.Loop() {
		_ = validateSoundLevelSettings(settings)
	}
}

// TestValidateDashboardSpeciesGuide covers the SpeciesGuide block of
// validateDashboardSettings. Provider must be non-empty when the feature is
// enabled, and must match a known provider when set. FallbackPolicy and
// WarmTopN are also covered to guard against regressions in the same block.
func TestValidateDashboardSpeciesGuide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		guide    SpeciesGuideConfig
		wantErr  bool
		errType  string
	}{
		{
			name: "enabled with empty provider - should fail",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: "",
			},
			wantErr: true,
			errType: "species-guide-provider-missing",
		},
		{
			name: "disabled with empty provider - should pass",
			guide: SpeciesGuideConfig{
				Enabled:  false,
				Provider: "",
			},
			wantErr: false,
		},
		{
			name: "enabled with wikipedia provider - should pass",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: SpeciesGuideProviderWikipedia,
			},
			wantErr: false,
		},
		{
			name: "enabled with ebird provider - should pass",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: SpeciesGuideProviderEBird,
			},
			wantErr: false,
		},
		{
			name: "enabled with auto provider - should pass",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: SpeciesGuideProviderAuto,
			},
			wantErr: false,
		},
		{
			name: "enabled with invalid provider - should fail",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: "mystery-provider",
			},
			wantErr: true,
			errType: "species-guide-provider",
		},
		{
			name: "disabled with invalid provider - should fail (provider still validated when set)",
			guide: SpeciesGuideConfig{
				Enabled:  false,
				Provider: "mystery-provider",
			},
			wantErr: true,
			errType: "species-guide-provider",
		},
		{
			name: "valid provider with invalid fallback policy - should fail",
			guide: SpeciesGuideConfig{
				Enabled:        true,
				Provider:       SpeciesGuideProviderWikipedia,
				FallbackPolicy: "sometimes",
			},
			wantErr: true,
			errType: "species-guide-fallback-policy",
		},
		{
			name: "valid provider with 'all' fallback - should pass",
			guide: SpeciesGuideConfig{
				Enabled:        true,
				Provider:       SpeciesGuideProviderWikipedia,
				FallbackPolicy: SpeciesGuideFallbackAll,
			},
			wantErr: false,
		},
		{
			name: "valid provider with 'none' fallback - should pass",
			guide: SpeciesGuideConfig{
				Enabled:        true,
				Provider:       SpeciesGuideProviderWikipedia,
				FallbackPolicy: SpeciesGuideFallbackNone,
			},
			wantErr: false,
		},
		{
			name: "negative WarmTopN - should fail",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: SpeciesGuideProviderWikipedia,
				WarmTopN: -1,
			},
			wantErr: true,
			errType: "species-guide-warm-top-n",
		},
		{
			name: "zero WarmTopN - should pass (feature disabled knob)",
			guide: SpeciesGuideConfig{
				Enabled:  true,
				Provider: SpeciesGuideProviderWikipedia,
				WarmTopN: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dashboard := &Dashboard{
				SpeciesGuide: tt.guide,
			}

			err := validateDashboardSettings(dashboard)

			if tt.wantErr {
				assertValidationError(t, err, tt.errType)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateDashboardSpeciesGuideErrorMessage ensures the missing-provider
// error mentions all valid providers so operators can fix their config
// without reading source.
func TestValidateDashboardSpeciesGuideErrorMessage(t *testing.T) {
	t.Parallel()

	dashboard := &Dashboard{
		SpeciesGuide: SpeciesGuideConfig{
			Enabled:  true,
			Provider: "",
		},
	}

	err := validateDashboardSettings(dashboard)
	require.Error(t, err, "expected error for enabled guide with empty provider")

	msg := err.Error()
	assert.Contains(t, msg, "required when species guide is enabled")
	assert.Contains(t, msg, SpeciesGuideProviderWikipedia)
	assert.Contains(t, msg, SpeciesGuideProviderEBird)
	assert.Contains(t, msg, SpeciesGuideProviderAuto)
}
