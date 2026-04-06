// internal/api/v2/species.go
package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// RarityStatus represents the rarity classification of a species
type RarityStatus string

const (
	RarityVeryCommon RarityStatus = "very_common"
	RarityCommon     RarityStatus = "common"
	RarityUncommon   RarityStatus = "uncommon"
	RarityRare       RarityStatus = "rare"
	RarityVeryRare   RarityStatus = "very_rare"
	RarityUnknown    RarityStatus = "unknown"
)

// Rarity threshold constants for score-based classification
const (
	RarityThresholdVeryCommon = 0.8
	RarityThresholdCommon     = 0.5
	RarityThresholdUncommon   = 0.2
	RarityThresholdRare       = 0.05
)

// maxNoteEntryLength is the maximum allowed length for a species note entry.
const maxNoteEntryLength = 10_000

// maxSimilarSpeciesResults is the maximum number of similar species to return.
const maxSimilarSpeciesResults = 5

// maxGuideSummaryLen is the maximum character length for a similar species guide summary.
const maxGuideSummaryLen = 200

// relationshipSameGenus is the relationship value for species in the same genus.
const relationshipSameGenus = "same_genus"

// Taxonomy classification constants for birds.
const (
	taxonomyKingdomAnimalia = "Animalia"
	taxonomyPhylumChordata  = "Chordata"
	taxonomyClassAves       = "Aves"
)

// Expectedness represents how expected a species is in the user's area at the current time.
type Expectedness string

const (
	ExpectednessExpected   Expectedness = "expected"
	ExpectednessUncommon   Expectedness = "uncommon"
	ExpectednessRare       Expectedness = "rare"
	ExpectednessUnexpected Expectedness = "unexpected"
)

// SpeciesInfo represents extended information about a bird species
type SpeciesInfo struct {
	ScientificName string              `json:"scientific_name"`
	CommonName     string              `json:"common_name"`
	Rarity         *SpeciesRarityInfo  `json:"rarity,omitempty"`
	Taxonomy       *ebird.TaxonomyTree `json:"taxonomy,omitempty"`
	Metadata       map[string]any      `json:"metadata,omitempty"`
}

// SpeciesRarityInfo contains rarity information for a species
type SpeciesRarityInfo struct {
	Status           RarityStatus `json:"status"`
	Score            float64      `json:"score"`
	LocationBased    bool         `json:"location_based"`
	Latitude         float64      `json:"latitude,omitempty"`
	Longitude        float64      `json:"longitude,omitempty"`
	Date             string       `json:"date"`
	ThresholdApplied float64      `json:"threshold_applied"`
}

// taxonomyLookupResult holds the result of a taxonomy lookup with source info.
type taxonomyLookupResult struct {
	tree   *ebird.TaxonomyTree
	source string
}

// lookupTaxonomyTree attempts to find taxonomy for a species, trying local DB first then eBird.
// Returns nil result (not error) if taxonomy is unavailable from both sources.
func (c *Controller) lookupTaxonomyTree(ctx context.Context, scientificName string) *taxonomyLookupResult {
	// Try local taxonomy database first (fast, no network)
	if c.TaxonomyDB != nil {
		tree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
		if err == nil {
			c.Debug("Retrieved taxonomy for %s from local database", scientificName)
			return &taxonomyLookupResult{tree: tree, source: "local"}
		}
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		tree, err := c.EBirdClient.BuildFamilyTree(ctx, scientificName)
		if err != nil {
			c.Debug("Failed to get taxonomy info from eBird for species %s: %v", scientificName, err)
			return nil
		}
		return &taxonomyLookupResult{tree: tree, source: "ebird"}
	}

	return nil
}

// initSpeciesRoutes registers all species-related API endpoints
func (c *Controller) initSpeciesRoutes() {
	// Public endpoints for species information
	c.Group.GET("/species", c.GetSpeciesInfo)
	c.Group.GET("/species/all", c.GetAllSpecies)
	c.Group.GET("/species/taxonomy", c.GetSpeciesTaxonomy)

	// RESTful thumbnail endpoint - uses species code from path
	c.Group.GET("/species/:code/thumbnail", c.GetSpeciesThumbnail)

	// Species guide endpoint
	c.Group.GET("/species/:scientific_name/guide", c.GetSpeciesGuide)

	// Similar species endpoint
	c.Group.GET("/species/:scientific_name/similar", c.GetSimilarSpecies)

	// Species notes endpoints
	c.Group.GET("/species/:scientific_name/notes", c.GetSpeciesNotes)
	c.Group.POST("/species/:scientific_name/notes", c.CreateSpeciesNote, c.authMiddleware)
	c.Group.DELETE("/species/notes/:id", c.DeleteSpeciesNote, c.authMiddleware)

	// New taxonomy endpoints using local database
	c.Group.GET("/taxonomy/genus/:genus", c.GetGenusSpecies)
	c.Group.GET("/taxonomy/family/:family", c.GetFamilySpecies)
	c.Group.GET("/taxonomy/tree/:scientific_name", c.GetSpeciesTree)
}

// AllSpeciesResponse represents the response for the all species endpoint
type AllSpeciesResponse struct {
	Species []RangeFilterSpecies `json:"species"`
	Count   int                  `json:"count"`
}

// GetAllSpecies returns all known BirdNET species labels regardless of location or range filter.
// This is used for species include/exclude search where users need to find any species,
// not just those matching the current location's range filter.
// @Summary Get all BirdNET species
// @Description Returns all species from the loaded BirdNET labels, independent of range filter
// @Tags species
// @Produce json
// @Success 200 {object} AllSpeciesResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/species/all [get]
func (c *Controller) GetAllSpecies(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	c.logDebugIfEnabled("Retrieving all BirdNET species labels",
		logger.String("ip", ip),
		logger.String("path", path),
	)

	labels := c.Settings.BirdNET.Labels
	speciesList := make([]RangeFilterSpecies, 0, len(labels))

	for _, label := range labels {
		sp := detection.ParseSpeciesString(label)
		speciesList = append(speciesList, RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
		})
	}

	c.logInfoIfEnabled("All species labels retrieved successfully",
		logger.Int("count", len(speciesList)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, AllSpeciesResponse{
		Species: speciesList,
		Count:   len(speciesList),
	})
}

// GetSpeciesInfo retrieves extended information about a bird species
func (c *Controller) GetSpeciesInfo(ctx echo.Context) error {
	// Get scientific name from query parameter
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get species info
	speciesInfo, err := c.getSpeciesInfo(ctx.Request().Context(), scientificName)
	if err != nil {
		return c.handleErrorWithNotFound(ctx, err, "Species not found", "Failed to get species information")
	}

	return ctx.JSON(http.StatusOK, speciesInfo)
}

// getSpeciesInfo retrieves species information including rarity status
func (c *Controller) getSpeciesInfo(ctx context.Context, scientificName string) (*SpeciesInfo, error) {
	// Get the BirdNET instance from the processor
	if c.Processor == nil || c.Processor.Bn == nil {
		return nil, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build()
	}

	bn := c.Processor.Bn

	// Find the full label for this species from BirdNET labels
	var matchedLabel string
	var commonName string

	for _, label := range bn.Settings.BirdNET.Labels {
		sp := detection.ParseSpeciesString(label)
		if strings.EqualFold(sp.ScientificName, scientificName) {
			matchedLabel = label
			commonName = sp.CommonName
			break
		}
	}

	// If species not found in labels, return error
	if matchedLabel == "" {
		return nil, errors.Newf("species '%s' not found in BirdNET labels", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create basic species info
	info := &SpeciesInfo{
		ScientificName: scientificName,
		CommonName:     commonName,
		Metadata:       make(map[string]any),
	}

	// Get rarity information
	rarityInfo, err := c.getSpeciesRarityInfo(bn, matchedLabel)
	if err != nil {
		// Log error but don't fail the request
		c.Debug("Failed to get rarity info for species %s: %v", scientificName, err)
		// Continue without rarity info
	} else {
		info.Rarity = rarityInfo
	}

	// Get taxonomy/family tree information using fallback pattern
	if result := c.lookupTaxonomyTree(ctx, scientificName); result != nil {
		info.Taxonomy = result.tree
		info.Metadata["source"] = result.source
	}

	return info, nil
}

// getSpeciesRarityInfo calculates the rarity status for a species
func (c *Controller) getSpeciesRarityInfo(bn *birdnet.BirdNET, speciesLabel string) (*SpeciesRarityInfo, error) {
	// Get current date
	today := time.Now().Truncate(HoursPerDay * time.Hour)

	// Get probable species with scores
	speciesScores, err := bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryProcessing).
			Context("species_label", speciesLabel).
			Component("api-species").
			Build()
	}

	// Create rarity info
	rarityInfo := &SpeciesRarityInfo{
		Date:             today.Format(time.DateOnly),
		LocationBased:    bn.Settings.BirdNET.LocationConfigured,
		ThresholdApplied: float64(bn.Settings.BirdNET.RangeFilter.Threshold),
	}

	// Add location if available
	if rarityInfo.LocationBased {
		rarityInfo.Latitude = bn.Settings.BirdNET.Latitude
		rarityInfo.Longitude = bn.Settings.BirdNET.Longitude
	}

	// Find the species score
	var score float64
	found := false
	for _, ss := range speciesScores {
		if ss.Label == speciesLabel {
			score = ss.Score
			found = true
			break
		}
	}

	// If not found in probable species, it's very rare
	if !found {
		rarityInfo.Status = RarityVeryRare
		rarityInfo.Score = 0.0
		return rarityInfo, nil
	}

	// Set score and calculate rarity status
	rarityInfo.Score = score
	rarityInfo.Status = calculateRarityStatus(score)

	return rarityInfo, nil
}

// calculateRarityStatus determines the rarity status based on the probability score
func calculateRarityStatus(score float64) RarityStatus {
	switch {
	case score > RarityThresholdVeryCommon:
		return RarityVeryCommon
	case score > RarityThresholdCommon:
		return RarityCommon
	case score > RarityThresholdUncommon:
		return RarityUncommon
	case score > RarityThresholdRare:
		return RarityRare
	default:
		return RarityVeryRare
	}
}

// buildExternalLinks generates curated links to external bird identification resources.
func buildExternalLinks(commonName string) []ExternalLink {
	if commonName == "" {
		return nil
	}

	// AllAboutBirds uses hyphenated lowercase common names as URL slugs.
	slug := strings.ToLower(commonName)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "'", "")

	return []ExternalLink{
		{
			Name: "All About Birds",
			URL:  "https://www.allaboutbirds.org/guide/" + slug,
		},
		{
			Name: "Xeno-canto",
			URL:  "https://xeno-canto.org/species/" + url.PathEscape(commonName),
		},
	}
}

// scoreToExpectedness maps a BirdNET probability score to an expectedness classification.
func scoreToExpectedness(score float64) Expectedness {
	switch {
	case score > RarityThresholdCommon:
		return ExpectednessExpected
	case score > RarityThresholdUncommon:
		return ExpectednessUncommon
	case score > RarityThresholdRare:
		return ExpectednessRare
	default:
		return ExpectednessUnexpected
	}
}

// computeCurrentSeason determines the current season name based on latitude and time.
// It uses the default season definitions for the detected hemisphere.
func computeCurrentSeason(latitude float64, now time.Time) string {
	seasons := conf.GetDefaultSeasons(latitude)

	// Build a sorted list of season boundaries for the current year.
	type seasonBoundary struct {
		name string
		date time.Time
	}

	boundaries := make([]seasonBoundary, 0, len(seasons))
	for name, s := range seasons {
		boundaries = append(boundaries, seasonBoundary{
			name: name,
			date: time.Date(now.Year(), time.Month(s.StartMonth), s.StartDay, 0, 0, 0, 0, now.Location()),
		})
	}

	// Sort by date ascending.
	slices.SortFunc(boundaries, func(a, b seasonBoundary) int {
		return a.date.Compare(b.date)
	})

	// Find the most recent boundary that has passed.
	currentSeason := boundaries[len(boundaries)-1].name // default: last season (wraps around)
	for _, b := range boundaries {
		if now.Before(b.date) {
			break
		}
		currentSeason = b.name
	}

	return currentSeason
}

// TaxonomyInfo represents detailed taxonomy information for a species
type TaxonomyInfo struct {
	ScientificName     string             `json:"scientific_name"`
	SpeciesCode        string             `json:"species_code,omitempty"`
	Taxonomy           *TaxonomyHierarchy `json:"taxonomy,omitempty"`
	Subspecies         []SubspeciesInfo   `json:"subspecies,omitempty"`
	Synonyms           []string           `json:"synonyms,omitempty"`
	ConservationStatus string             `json:"conservation_status,omitempty"`
	NativeRegions      []string           `json:"native_regions,omitempty"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
}

// TaxonomyHierarchy represents the full taxonomic classification
type TaxonomyHierarchy struct {
	Kingdom       string `json:"kingdom"`
	Phylum        string `json:"phylum"`
	Class         string `json:"class"`
	Order         string `json:"order"`
	Family        string `json:"family"`
	FamilyCommon  string `json:"family_common,omitempty"`
	Genus         string `json:"genus"`
	Species       string `json:"species"`
	SpeciesCommon string `json:"species_common,omitempty"`
}

// SubspeciesInfo represents information about a subspecies
type SubspeciesInfo struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name,omitempty"`
	Region         string `json:"region,omitempty"`
}

// GetSpeciesTaxonomy retrieves detailed taxonomy information for a species
func (c *Controller) GetSpeciesTaxonomy(ctx echo.Context) error {
	// Get parameters from query
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get optional parameters
	locale := ctx.QueryParam("locale")
	includeSubspecies := ctx.QueryParam("include_subspecies") != "false" // default true
	includeHierarchy := ctx.QueryParam("include_hierarchy") != "false"   // default true

	// Get taxonomy info
	taxonomyInfo, err := c.getDetailedTaxonomy(ctx.Request().Context(), scientificName, locale, includeSubspecies, includeHierarchy)
	if err != nil {
		return c.handleErrorWithNotFound(ctx, err, "Species not found", "Failed to get taxonomy information")
	}

	return ctx.JSON(http.StatusOK, taxonomyInfo)
}

// getDetailedTaxonomy retrieves detailed taxonomy information
// Tries local database first, falls back to eBird API if needed
func (c *Controller) getDetailedTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) (*TaxonomyInfo, error) {
	// Try local taxonomy database first
	if info := c.tryLocalTaxonomy(ctx, scientificName, locale, includeSubspecies, includeHierarchy); info != nil {
		return info, nil
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		return c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	}

	// Neither local DB nor eBird API available
	return nil, errors.Newf("taxonomy data not available (no local database or eBird API)").
		Category(errors.CategoryConfiguration).
		Priority(errors.PriorityLow).
		Context("scientific_name", scientificName).
		Component("api-species").
		Build()
}

// tryLocalTaxonomy attempts to retrieve taxonomy from the local database.
// Returns nil if local DB is unavailable or lookup fails.
func (c *Controller) tryLocalTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) *TaxonomyInfo {
	if c.TaxonomyDB == nil {
		return nil
	}

	taxonomyTree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
	if err != nil {
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
		return nil
	}

	info := &TaxonomyInfo{
		ScientificName: scientificName,
		Metadata: map[string]any{
			"source":     "local",
			"updated_at": c.TaxonomyDB.UpdatedAt,
		},
	}

	// Add hierarchy if requested
	if includeHierarchy && taxonomyTree != nil {
		info.Taxonomy = convertToTaxonomyHierarchy(taxonomyTree)
	}

	// Enhance with eBird data if needed
	c.enhanceWithEBirdData(ctx, info, scientificName, locale, includeSubspecies)

	return info
}

// convertToTaxonomyHierarchy converts an ebird.TaxonomyTree to TaxonomyHierarchy.
func convertToTaxonomyHierarchy(tree *ebird.TaxonomyTree) *TaxonomyHierarchy {
	return &TaxonomyHierarchy{
		Kingdom:       tree.Kingdom,
		Phylum:        tree.Phylum,
		Class:         tree.Class,
		Order:         tree.Order,
		Family:        tree.Family,
		FamilyCommon:  tree.FamilyCommon,
		Genus:         tree.Genus,
		Species:       tree.Species,
		SpeciesCommon: tree.SpeciesCommon,
	}
}

// enhanceWithEBirdData adds subspecies and locale data from eBird API to local taxonomy info.
func (c *Controller) enhanceWithEBirdData(ctx context.Context, info *TaxonomyInfo, scientificName, locale string, includeSubspecies bool) {
	if c.EBirdClient == nil || (!includeSubspecies && locale == "") {
		return
	}

	c.Debug("Enhancing local taxonomy data with eBird API for subspecies/locale")
	ebirdInfo, err := c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	if err != nil {
		return
	}

	if includeSubspecies && len(ebirdInfo.Subspecies) > 0 {
		info.Subspecies = ebirdInfo.Subspecies
	}
	if ebirdInfo.SpeciesCode != "" {
		info.SpeciesCode = ebirdInfo.SpeciesCode
	}
	info.Metadata["source"] = "local+ebird"
	if locale != "" {
		info.Metadata["locale"] = locale
	}
}

// getEBirdTaxonomy retrieves taxonomy information from eBird API
func (c *Controller) getEBirdTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies bool) (*TaxonomyInfo, error) {
	// Get full taxonomy data with locale if specified
	taxonomyData, err := c.EBirdClient.GetTaxonomy(ctx, locale)
	if err != nil {
		return nil, err
	}

	// Find the species in taxonomy
	var speciesEntry *ebird.TaxonomyEntry
	for i := range taxonomyData {
		if strings.EqualFold(taxonomyData[i].ScientificName, scientificName) {
			speciesEntry = &taxonomyData[i]
			break
		}
	}

	if speciesEntry == nil {
		return nil, errors.Newf("species '%s' not found in eBird taxonomy", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create taxonomy info
	info := &TaxonomyInfo{
		ScientificName: speciesEntry.ScientificName,
		SpeciesCode:    speciesEntry.SpeciesCode,
		Metadata: map[string]any{
			"source":     "ebird",
			"updated_at": time.Now().Format(time.RFC3339),
			"locale":     locale,
		},
	}

	// Parse genus from scientific name
	parts := strings.Fields(speciesEntry.ScientificName)
	genus := ""
	if len(parts) > 0 {
		genus = parts[0]
	}

	info.Taxonomy = &TaxonomyHierarchy{
		Kingdom:       taxonomyKingdomAnimalia,
		Phylum:        taxonomyPhylumChordata,
		Class:         taxonomyClassAves,
		Order:         speciesEntry.Order,
		Family:        speciesEntry.FamilySciName,
		FamilyCommon:  speciesEntry.FamilyComName,
		Genus:         genus,
		Species:       speciesEntry.ScientificName,
		SpeciesCommon: speciesEntry.CommonName,
	}

	// Add subspecies if requested and it's a species entry
	if includeSubspecies && speciesEntry.Category == "species" {
		subspecies := c.findDetailedSubspecies(taxonomyData, speciesEntry.SpeciesCode)
		info.Subspecies = subspecies
	}

	// TODO: Add conservation status and native regions when available from eBird API

	return info, nil
}

// findDetailedSubspecies finds all subspecies with detailed information
func (c *Controller) findDetailedSubspecies(taxonomy []ebird.TaxonomyEntry, speciesCode string) []SubspeciesInfo {
	var subspecies []SubspeciesInfo //nolint:prealloc // subspecies count requires full scan to determine

	for i := range taxonomy {
		// Check if this entry reports as our species and is a subspecies category
		if taxonomy[i].ReportAs == speciesCode &&
			(taxonomy[i].Category == "issf" || taxonomy[i].Category == "form") {

			// Extract region from common name if present (often in parentheses)
			region := ""
			commonName := taxonomy[i].CommonName
			if start := strings.Index(commonName, "("); start != -1 {
				if end := strings.Index(commonName[start:], ")"); end != -1 {
					region = strings.TrimSpace(commonName[start+1 : start+end])
				}
			}

			subspecies = append(subspecies, SubspeciesInfo{
				ScientificName: taxonomy[i].ScientificName,
				CommonName:     taxonomy[i].CommonName,
				Region:         region,
			})
		}
	}

	return subspecies
}

// GetSpeciesThumbnail retrieves a bird thumbnail image by species code
// GET /api/v2/species/:code/thumbnail
func (c *Controller) GetSpeciesThumbnail(ctx echo.Context) error {
	speciesCode := ctx.Param("code")
	if speciesCode == "" {
		return c.HandleError(ctx, errors.Newf("species code parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing species code", http.StatusBadRequest)
	}

	// Log the request if API logger is available
	c.logDebugIfEnabled("Retrieving thumbnail for species code",
		logger.String("species_code", speciesCode),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Check if BirdNET processor is available
	if c.Processor == nil || c.Processor.Bn == nil {
		return c.HandleError(ctx, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "BirdNET service unavailable", http.StatusServiceUnavailable)
	}

	// Check if BirdImageCache is available
	if c.BirdImageCache == nil {
		return c.HandleError(ctx, errors.Newf("image service unavailable").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "Image service unavailable", http.StatusServiceUnavailable)
	}

	// Get species name from the taxonomy map using the species code
	bn := c.Processor.Bn
	speciesName, exists := birdnet.GetSpeciesNameFromCode(bn.TaxonomyMap, speciesCode)

	if !exists {
		return c.HandleError(ctx, errors.Newf("species code '%s' not found in taxonomy", speciesCode).
			Category(errors.CategoryNotFound).
			Context("species_code", speciesCode).
			Component("api-species").
			Build(), "Species not found", http.StatusNotFound)
	}

	// Split the species name to get scientific name
	scientificName, _ := birdnet.SplitSpeciesName(speciesName)

	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("invalid species name format for code '%s'", speciesCode).
			Category(errors.CategoryValidation).
			Context("species_code", speciesCode).
			Context("species_name", speciesName).
			Component("api-species").
			Build(), "Invalid species data", http.StatusInternalServerError)
	}

	// Delegate to the image proxy handler
	ctx.SetParamNames("scientific_name")
	ctx.SetParamValues(scientificName)
	return c.ServeSpeciesImageProxy(ctx)
}

// GuideQuality indicates the richness of guide content.
type GuideQuality string

const (
	// GuideQualityFull means the guide has structured sections (Description, Songs, etc.).
	GuideQualityFull GuideQuality = "full"
	// GuideQualityIntroOnly means only the intro paragraph is available.
	GuideQualityIntroOnly GuideQuality = "intro_only"
	// GuideQualityStub means only metadata is available, no description.
	GuideQualityStub GuideQuality = "stub"
)

// classifyGuideQuality determines the quality level of guide content.
func classifyGuideQuality(description string, partial bool) GuideQuality {
	if partial || description == "" {
		return GuideQualityStub
	}
	if strings.Contains(description, "## ") {
		return GuideQualityFull
	}
	return GuideQualityIntroOnly
}

// ExternalLink represents a curated link to an external resource.
type ExternalLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// GuideFeatureFlags indicates which optional guide features are enabled.
type GuideFeatureFlags struct {
	Notes          bool `json:"notes"`
	Enrichments    bool `json:"enrichments"`
	SimilarSpecies bool `json:"similar_species"`
}

// SpeciesGuideResponse represents the API response for a species guide.
type SpeciesGuideResponse struct {
	ScientificName     string             `json:"scientific_name"`
	CommonName         string             `json:"common_name"`
	Description        string             `json:"description"`
	ConservationStatus string             `json:"conservation_status"`
	Quality            GuideQuality       `json:"quality"`
	Expectedness       Expectedness       `json:"expectedness,omitempty"`
	CurrentSeason      string             `json:"current_season,omitempty"`
	ExternalLinks      []ExternalLink     `json:"external_links,omitempty"`
	Features           GuideFeatureFlags  `json:"features"`
	Source             SpeciesGuideSource `json:"source"`
	Partial            bool               `json:"partial"`
	CachedAt           time.Time          `json:"cached_at"`
}

// SpeciesGuideSource represents the attribution for the guide data.
type SpeciesGuideSource struct {
	Provider   string `json:"provider"`
	URL        string `json:"url"`
	License    string `json:"license"`
	LicenseURL string `json:"license_url"`
}

// GetSpeciesGuide retrieves guide text for a species.
// @Summary Get species guide information
// @Description Returns textual guide information (description, conservation status) for a species
// @Tags species
// @Produce json
// @Param scientific_name path string true "Scientific name (URL-encoded)"
// @Param locale query string false "Wikipedia language code (e.g. de, fr, es). Defaults to en."
// @Success 200 {object} SpeciesGuideResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /api/v2/species/{scientific_name}/guide [get]
func (c *Controller) GetSpeciesGuide(ctx echo.Context) error {
	// Check if guide feature is enabled
	if !c.Settings.Realtime.Dashboard.SpeciesGuide.Enabled {
		return c.HandleError(ctx, errors.Newf("species guide feature is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Species guide feature is disabled", http.StatusNotFound)
	}

	// Check if guide cache is available (read under lock for hot-reload safety).
	gc := c.GetGuideCache()
	if gc == nil {
		return c.HandleError(ctx, errors.Newf("species guide not available").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Species guide service not available", http.StatusServiceUnavailable)
	}

	// Get and validate scientific name from path parameter
	rawName := ctx.Param("scientific_name")
	scientificName, err := url.PathUnescape(rawName)
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid scientific name encoding").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Invalid scientific name", http.StatusBadRequest)
	}

	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Parse optional locale query parameter for Wikipedia language selection
	locale := strings.TrimSpace(ctx.QueryParam("locale"))

	// Fetch guide from cache (memory → DB → providers)
	guide, err := gc.Get(ctx.Request().Context(), scientificName, guideprovider.FetchOptions{Locale: locale})
	if err != nil {
		if errors.Is(err, guideprovider.ErrGuideNotFound) {
			return c.HandleError(ctx, err, "Species guide not found", http.StatusNotFound)
		}
		if errors.Is(err, guideprovider.ErrAllProvidersUnavailable) {
			return c.HandleError(ctx, err, "Guide service temporarily unavailable", http.StatusServiceUnavailable)
		}
		return c.HandleError(ctx, err, "Failed to retrieve species guide", http.StatusInternalServerError)
	}

	response := SpeciesGuideResponse{
		ScientificName:     guide.ScientificName,
		CommonName:         guide.CommonName,
		Description:        guide.Description,
		ConservationStatus: guide.ConservationStatus,
		Quality:            classifyGuideQuality(guide.Description, guide.Partial),
		Source: SpeciesGuideSource{
			Provider:   guide.SourceProvider,
			URL:        guide.SourceURL,
			License:    guide.LicenseName,
			LicenseURL: guide.LicenseURL,
		},
		Partial:  guide.Partial,
		CachedAt: guide.CachedAt,
	}

	// Include feature flags so the frontend knows what to render.
	guideConfig := c.Settings.Realtime.Dashboard.SpeciesGuide
	response.Features = GuideFeatureFlags{
		Notes:          guideConfig.IsShowNotes(),
		Enrichments:    guideConfig.IsShowEnrichments(),
		SimilarSpecies: guideConfig.IsShowSimilarSpecies(),
	}

	// Add enrichments (expectedness, season, external links) if enabled.
	if guideConfig.IsShowEnrichments() {
		latitude := c.Settings.BirdNET.Latitude
		now := time.Now()
		response.CurrentSeason = computeCurrentSeason(latitude, now)
		response.ExternalLinks = buildExternalLinks(guide.CommonName)

		if c.Processor != nil {
			if bn := c.Processor.GetBirdNET(); bn != nil {
				speciesScores, scoreErr := bn.GetProbableSpecies(now, 0.0)
				if scoreErr == nil {
					found := false
					for _, ss := range speciesScores {
						if detection.ParseSpeciesString(ss.Label).ScientificName == scientificName {
							response.Expectedness = scoreToExpectedness(ss.Score)
							found = true
							break
						}
					}
					if !found {
						response.Expectedness = ExpectednessUnexpected
					}
				}
			}
		}
	}

	return ctx.JSON(http.StatusOK, response)
}

// SimilarSpeciesEntry represents one similar species in the comparison response.
type SimilarSpeciesEntry struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	Relationship   string `json:"relationship"` // "same_genus", "same_family", or "similar"
	GuideSummary   string `json:"guide_summary,omitempty"`
}

// SimilarSpeciesResponse is the response for the similar species endpoint.
type SimilarSpeciesResponse struct {
	ScientificName string                 `json:"scientific_name"`
	Genus          string                 `json:"genus"`
	Similar        []SimilarSpeciesEntry  `json:"similar"`
}

// GetSimilarSpecies returns species that are similar or related to the given species.
// @Summary Get similar species
// @Description Returns up to 5 species in the same genus, with optional guide summaries
// @Tags species
// @Produce json
// @Param scientific_name path string true "Scientific name (URL-encoded)"
// @Param locale query string false "Wikipedia language code for guide summaries"
// @Success 200 {object} SimilarSpeciesResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v2/species/{scientific_name}/similar [get]
func (c *Controller) GetSimilarSpecies(ctx echo.Context) error {
	if !c.Settings.Realtime.Dashboard.SpeciesGuide.IsShowSimilarSpecies() {
		return c.HandleError(ctx, errors.Newf("similar species feature is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Similar species feature is disabled", http.StatusNotFound)
	}

	rawName := ctx.Param("scientific_name")
	scientificName, err := url.PathUnescape(rawName)
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid scientific name encoding").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Invalid scientific name", http.StatusBadRequest)
	}

	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Extract genus (first word of the binomial name).
	genus := scientificName
	if spaceIdx := strings.IndexByte(scientificName, ' '); spaceIdx > 0 {
		genus = scientificName[:spaceIdx]
	}

	// Find same-genus species from BirdNET labels.
	similar := make([]SimilarSpeciesEntry, 0, maxSimilarSpeciesResults)
	if c.Processor != nil {
		if bn := c.Processor.GetBirdNET(); bn != nil {
			genusPrefix := genus + " "
			for _, label := range bn.Settings.BirdNET.Labels {
				sp := detection.ParseSpeciesString(label)
				if sp.ScientificName == scientificName {
					continue // skip self
				}
				if strings.HasPrefix(sp.ScientificName, genusPrefix) {
					similar = append(similar, SimilarSpeciesEntry{
						ScientificName: sp.ScientificName,
						CommonName:     sp.CommonName,
						Relationship:   relationshipSameGenus,
					})
				}
				if len(similar) >= maxSimilarSpeciesResults {
					break
				}
			}
		}
	}

	// Optionally fetch short guide summaries for each similar species.
	locale := strings.TrimSpace(ctx.QueryParam("locale"))
	if gc := c.GetGuideCache(); gc != nil && len(similar) > 0 {
		for i := range similar {
			guide, guideErr := gc.Get(ctx.Request().Context(), similar[i].ScientificName, guideprovider.FetchOptions{Locale: locale})
			if guideErr == nil && guide.Description != "" {
				// Truncate to a short summary for the similar species list.
				summary := guide.Description
				if len(summary) > maxGuideSummaryLen {
					summary = summary[:maxGuideSummaryLen] + "…"
				}
				similar[i].GuideSummary = summary
			}
		}
	}

	response := SimilarSpeciesResponse{
		ScientificName: scientificName,
		Genus:          genus,
		Similar:        similar,
	}

	return ctx.JSON(http.StatusOK, response)
}

// SpeciesNoteResponse represents a species note in API responses.
type SpeciesNoteResponse struct {
	ID        uint      `json:"id"`
	Entry     string    `json:"entry"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateSpeciesNoteRequest represents the request body for creating a species note.
type CreateSpeciesNoteRequest struct {
	Entry string `json:"entry"`
}

// GetSpeciesNotes retrieves all notes for a species.
// @Summary Get species notes
// @Description Returns user-authored notes for a species
// @Tags species
// @Produce json
// @Param scientific_name path string true "Scientific name (URL-encoded)"
// @Success 200 {array} SpeciesNoteResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/v2/species/{scientific_name}/notes [get]
func (c *Controller) GetSpeciesNotes(ctx echo.Context) error {
	if !c.Settings.Realtime.Dashboard.SpeciesGuide.IsShowNotes() {
		return c.HandleError(ctx, errors.Newf("species notes feature is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Species notes feature is disabled", http.StatusNotFound)
	}

	rawName := ctx.Param("scientific_name")
	scientificName, err := url.PathUnescape(rawName)
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid scientific name encoding").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Invalid scientific name", http.StatusBadRequest)
	}

	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	notes, err := c.DS.GetSpeciesNotes(scientificName)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to retrieve species notes", http.StatusInternalServerError)
	}

	response := make([]SpeciesNoteResponse, 0, len(notes))
	for _, n := range notes {
		response = append(response, SpeciesNoteResponse{
			ID:        n.ID,
			Entry:     n.Entry,
			CreatedAt: n.CreatedAt,
			UpdatedAt: n.UpdatedAt,
		})
	}

	return ctx.JSON(http.StatusOK, response)
}

// CreateSpeciesNote creates a new note for a species.
// @Summary Create a species note
// @Description Creates a new user note for a species
// @Tags species
// @Accept json
// @Produce json
// @Param scientific_name path string true "Scientific name (URL-encoded)"
// @Param body body CreateSpeciesNoteRequest true "Note content"
// @Success 201 {object} SpeciesNoteResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/v2/species/{scientific_name}/notes [post]
func (c *Controller) CreateSpeciesNote(ctx echo.Context) error {
	if !c.Settings.Realtime.Dashboard.SpeciesGuide.IsShowNotes() {
		return c.HandleError(ctx, errors.Newf("species notes feature is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Species notes feature is disabled", http.StatusNotFound)
	}

	rawName := ctx.Param("scientific_name")
	scientificName, err := url.PathUnescape(rawName)
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid scientific name encoding").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Invalid scientific name", http.StatusBadRequest)
	}

	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	var req CreateSpeciesNoteRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, errors.Newf("invalid request body: %w", err).
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Invalid request body", http.StatusBadRequest)
	}

	entry := strings.TrimSpace(req.Entry)
	if entry == "" {
		return c.HandleError(ctx, errors.Newf("entry cannot be empty").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Note entry is required", http.StatusBadRequest)
	}
	if len(entry) > maxNoteEntryLength {
		return c.HandleError(ctx, errors.Newf("note entry exceeds maximum length of %d characters", maxNoteEntryLength).
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Note entry too long", http.StatusBadRequest)
	}

	note := &datastore.SpeciesNote{
		ScientificName: scientificName,
		Entry:          entry,
	}

	if err := c.DS.SaveSpeciesNote(note); err != nil {
		return c.HandleError(ctx, err, "Failed to save species note", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusCreated, SpeciesNoteResponse{
		ID:        note.ID,
		Entry:     note.Entry,
		CreatedAt: note.CreatedAt,
		UpdatedAt: note.UpdatedAt,
	})
}

// DeleteSpeciesNote deletes a species note by ID.
// @Summary Delete a species note
// @Description Deletes a user note for a species
// @Tags species
// @Param id path string true "Note ID"
// @Success 204
// @Failure 400 {object} ErrorResponse
// @Router /api/v2/species/notes/{id} [delete]
func (c *Controller) DeleteSpeciesNote(ctx echo.Context) error {
	if !c.Settings.Realtime.Dashboard.SpeciesGuide.IsShowNotes() {
		return c.HandleError(ctx, errors.Newf("species notes feature is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build(), "Species notes feature is disabled", http.StatusNotFound)
	}

	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, errors.Newf("note ID is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing note ID", http.StatusBadRequest)
	}

	if err := c.DS.DeleteSpeciesNote(noteID); err != nil {
		return c.HandleError(ctx, err, "Failed to delete species note", http.StatusInternalServerError)
	}

	return ctx.NoContent(http.StatusNoContent)
}
