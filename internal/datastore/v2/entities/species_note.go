package entities

import "time"

// SpeciesNote stores user-authored notes about a species (not a single detection).
// Each species can have multiple notes.
type SpeciesNote struct {
	ID             uint      `gorm:"primaryKey"`
	ScientificName string    `gorm:"index;not null"`
	Entry          string    `gorm:"type:text;not null"`
	CreatedAt      time.Time `gorm:"autoCreateTime;index"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}
