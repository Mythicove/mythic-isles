package isles

import (
	"errors"
	"time"
)

type TownyReference struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type TownyTown struct {
	TownyReference
	Residents         int             `json:"residents"`
	ClaimedTownBlocks int             `json:"claimedTownBlocks"`
	Open              bool            `json:"open"`
	Public            bool            `json:"public"`
	Nation            *TownyReference `json:"nation,omitempty"`
}
type TownyNation struct {
	TownyReference
	Towns     int  `json:"towns"`
	Residents int  `json:"residents"`
	Open      bool `json:"open"`
	Public    bool `json:"public"`
}
type TownyResident struct {
	UUID        string     `json:"uuid"`
	Mayor       bool       `json:"mayor"`
	King        bool       `json:"king"`
	TownRanks   []string   `json:"townRanks"`
	NationRanks []string   `json:"nationRanks"`
	Town        *TownyTown `json:"town,omitempty"`
	ObservedAt  time.Time  `json:"observedAt"`
}
type TownySnapshot struct {
	TownCount   int             `json:"townCount"`
	NationCount int             `json:"nationCount"`
	Towns       []TownyTown     `json:"towns"`
	Nations     []TownyNation   `json:"nations"`
	Residents   []TownyResident `json:"residents"`
	ObservedAt  time.Time       `json:"observedAt"`
}

func validTownyReference(v TownyReference) bool {
	return len(v.ID) >= 16 && len(v.ID) <= 64 && v.Name != "" && len(v.Name) <= 256
}
func validTownyTown(v TownyTown) bool {
	return validTownyReference(v.TownyReference) && v.Residents >= 0 && v.ClaimedTownBlocks >= 0 && (v.Nation == nil || validTownyReference(*v.Nation))
}
func validateTownySnapshot(s *TownySnapshot) error {
	if s == nil {
		return nil
	}
	invalid := errors.New("invalid Towny observation")
	if s.Towns == nil || s.Nations == nil || s.Residents == nil || s.TownCount < len(s.Towns) || s.NationCount < len(s.Nations) || len(s.Towns) > 100 || len(s.Nations) > 100 || len(s.Residents) > 2000 || s.ObservedAt.IsZero() {
		return invalid
	}
	seen := map[string]bool{}
	for _, v := range s.Towns {
		if !validTownyTown(v) || seen[v.ID] {
			return invalid
		}
		seen[v.ID] = true
	}
	seen = map[string]bool{}
	for _, v := range s.Nations {
		if !validTownyReference(v.TownyReference) || v.Towns < 0 || v.Residents < 0 || seen[v.ID] {
			return invalid
		}
		seen[v.ID] = true
	}
	seen = map[string]bool{}
	for _, v := range s.Residents {
		if len(v.UUID) < 16 || len(v.UUID) > 64 || seen[v.UUID] || v.ObservedAt.IsZero() || v.TownRanks == nil || v.NationRanks == nil || len(v.TownRanks) > 32 || len(v.NationRanks) > 32 || (v.Town != nil && !validTownyTown(*v.Town)) {
			return invalid
		}
		seen[v.UUID] = true
		for _, ranks := range [][]string{v.TownRanks, v.NationRanks} {
			for _, rank := range ranks {
				if rank == "" || len(rank) > 120 {
					return invalid
				}
			}
		}
	}
	return nil
}
