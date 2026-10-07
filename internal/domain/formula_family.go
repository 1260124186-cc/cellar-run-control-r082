package domain

import "time"

// FormulaFamily groups every approved revision of a named formula. The family
// owns the unique normalized name. Exactly one revision is the current runnable
// version; at most one draft revision may be open at a time.
type FormulaFamily struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	CurrentID     *string   `json:"current_formula_id,omitempty"`
	DraftID       *string   `json:"draft_formula_id,omitempty"`
	NextRevision  int       `json:"next_revision_number"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RevisionContent holds the editable content shared by a first draft and a
// revision candidate. nil pointers mean "inherit from the source revision".
type RevisionContent struct {
	Name                  *string
	Style                 *string
	TargetOriginalGravity *float64
	TargetFinalGravity    *float64
	MaxFermentationTempC  *float64
	MinimumDays           *int
	MaximumDays           *int
	Steps                 *[]FormulaStep
}

// OpenDraft marks a newly created revision candidate as the family's single
// open draft. A family that already has a draft cannot open another one.
func (family *FormulaFamily) OpenDraft(draftID string, now time.Time) error {
	if family.DraftID != nil {
		return NewError(CodeFormulaRevisionOpen,
			"an open draft revision already exists for this formula")
	}
	stamp := now.UTC()
	draft := draftID
	family.DraftID = &draft
	family.NextRevision++
	family.UpdatedAt = stamp
	return nil
}

// Publish moves the approved candidate into the current slot and clears the
// open draft marker. The previous current version is returned to the caller so
// it can be marked superseded within the same atomic update.
func (family *FormulaFamily) Publish(candidateID string, now time.Time) *string {
	previous := family.CurrentID
	current := candidateID
	family.CurrentID = &current
	family.DraftID = nil
	family.UpdatedAt = now.UTC()
	if previous != nil && *previous == candidateID {
		return nil
	}
	return previous
}

// IsCurrent reports whether the given revision identity is the family's current
// runnable version.
func (family FormulaFamily) IsCurrent(formulaID string) bool {
	return family.CurrentID != nil && *family.CurrentID == formulaID
}

// HasDraft reports whether the family currently has an open draft revision.
func (family FormulaFamily) HasDraft() bool {
	return family.DraftID != nil
}
