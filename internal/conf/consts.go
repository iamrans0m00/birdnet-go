// conf/consts.go hard coded constants
package conf

import "slices"

// Species guide provider and fallback-policy constants.
//
// These are defined here rather than in the guideprovider package so that the
// conf package can validate SpeciesGuideConfig without importing guideprovider
// (which would create a circular dependency: guideprovider already imports conf).
// Any package that imports conf can use these constants directly.
const (
	SpeciesGuideProviderWikipedia = "wikipedia" // Wikipedia REST API provider
	SpeciesGuideProviderEBird     = "ebird"     // eBird taxonomy enrichment provider
	SpeciesGuideProviderAuto      = "auto"      // Auto-select provider based on availability

	SpeciesGuideFallbackAll  = "all"  // Try all providers in order on failure
	SpeciesGuideFallbackNone = "none" // No fallback; fail if primary provider fails
)

// speciesGuideValidProviders enumerates every recognized value for
// SpeciesGuideConfig.Provider. Defined as a package-level slice so validation
// avoids reallocating the list on every call. Treat as read-only.
var speciesGuideValidProviders = []string{
	SpeciesGuideProviderWikipedia,
	SpeciesGuideProviderEBird,
	SpeciesGuideProviderAuto,
}

// GetSpeciesGuideValidProviders returns a defensive copy of the valid provider list.
func GetSpeciesGuideValidProviders() []string {
	return slices.Clone(speciesGuideValidProviders)
}

// speciesGuideValidFallbackPolicies enumerates every recognized value for
// SpeciesGuideConfig.FallbackPolicy. Defined as a package-level slice so
// validation avoids reallocating the list on every call. Treat as read-only.
var speciesGuideValidFallbackPolicies = []string{
	SpeciesGuideFallbackAll,
	SpeciesGuideFallbackNone,
}

// GetSpeciesGuideValidFallbackPolicies returns a defensive copy of the valid fallback-policy list.
func GetSpeciesGuideValidFallbackPolicies() []string {
	return slices.Clone(speciesGuideValidFallbackPolicies)
}

const (
	SampleRate    = 48000 // Sample rate of the audio fed to BirdNET Analyzer
	BitDepth      = 16    // Bit depth of the audio fed to BirdNET Analyzer
	NumChannels   = 1     // Number of channels of the audio fed to BirdNET Analyzer
	CaptureLength = 3     // Length of audio data fed to BirdNET Analyzer in seconds

	SpeciesConfigCSV  = "species_config.csv"
	SpeciesActionsCSV = "species_actions.csv"

	// BufferSize is the size of the audio buffer in bytes, rounded up to the nearest 2048
	BufferSize = ((SampleRate*NumChannels*CaptureLength*BitDepth/8 + 2047) / 2048) * 2048

	// DefaultCaptureBufferSeconds is the default ring buffer duration when extended capture is disabled.
	// Audio.Export.Length must not exceed this value or audio export will be truncated.
	DefaultCaptureBufferSeconds = 120

	// Extended capture defaults
	DefaultExtendedCaptureMaxDuration = 120  // Default max duration in seconds (2 minutes)
	MaxExtendedCaptureDuration        = 1200 // Absolute max (20 minutes)
	ExtendedCaptureBufferMargin       = 60   // Margin added to MaxDuration for buffer sizing
	ExtendedCaptureMinBufferMargin    = 30   // Minimum margin above MaxDuration + PreCapture
)
