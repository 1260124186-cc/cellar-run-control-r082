package domain

import (
	"math"
	"strings"
	"time"
)

type FormulaState string

const (
	FormulaDraft    FormulaState = "draft"
	FormulaApproved FormulaState = "approved"
	FormulaRetired  FormulaState = "retired"
)

type FormulaStep struct {
	Sequence     int     `json:"sequence"`
	Label        string  `json:"label"`
	TemperatureC float64 `json:"temperature_c"`
	HoldMinutes  int     `json:"hold_minutes"`
}

type Formula struct {
	ID                    string        `json:"id"`
	FamilyID              string        `json:"family_id"`
	RevisionNumber        int           `json:"revision_number"`
	RevisionOfID          *string       `json:"revision_of_formula_id,omitempty"`
	SourceRevisionNumber  *int          `json:"source_revision_number,omitempty"`
	Name                  string        `json:"name"`
	Style                 string        `json:"style"`
	TargetOriginalGravity float64       `json:"target_original_gravity"`
	TargetFinalGravity    float64       `json:"target_final_gravity"`
	MaxFermentationTempC  float64       `json:"max_fermentation_temp_c"`
	MinimumDays           int           `json:"minimum_days"`
	MaximumDays           int           `json:"maximum_days"`
	Steps                 []FormulaStep `json:"steps"`
	State                 FormulaState  `json:"state"`
	Version               int64         `json:"version"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
	ApprovedAt            *time.Time    `json:"approved_at,omitempty"`
	SupersededAt          *time.Time    `json:"superseded_at,omitempty"`
	RetiredAt             *time.Time    `json:"retired_at,omitempty"`
}

func (f Formula) Clone() Formula {
	cloned := f
	cloned.Steps = append([]FormulaStep(nil), f.Steps...)
	return cloned
}

func (f *Formula) Normalize() {
	f.Name = NormalizeName(f.Name)
	f.Style = NormalizeName(f.Style)
}

func (f Formula) Validate() error {
	if f.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(f.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	if f.Style == "" {
		return FieldError(CodeInvalidInput, "style", "is required")
	}
	if len([]rune(f.Style)) > 80 {
		return FieldError(CodeInvalidInput, "style", "must not exceed 80 characters")
	}
	if f.TargetOriginalGravity < 1.030 || f.TargetOriginalGravity > 1.120 {
		return FieldError(CodeInvalidInput, "target_original_gravity",
			"must be between 1.030 and 1.120")
	}
	if f.TargetFinalGravity <= 0 || f.TargetOriginalGravity-f.TargetFinalGravity < 0.005 {
		return FieldError(CodeInvalidInput, "target_final_gravity",
			"must be positive and at least 0.005 below target original gravity")
	}
	if f.TargetFinalGravity > f.TargetOriginalGravity {
		return FieldError(CodeInvalidInput, "target_final_gravity",
			"must not exceed target original gravity")
	}
	if f.MaxFermentationTempC < 5 || f.MaxFermentationTempC > 40 {
		return FieldError(CodeInvalidInput, "max_fermentation_temp_c",
			"must be between 5 and 40 Celsius")
	}
	if f.MinimumDays < 1 || f.MinimumDays > 120 {
		return FieldError(CodeInvalidInput, "minimum_days",
			"must be between 1 and 120")
	}
	if f.MaximumDays < f.MinimumDays || f.MaximumDays > 120 {
		return FieldError(CodeInvalidInput, "maximum_days",
			"must be between minimum days and 120")
	}
	if len(f.Steps) > 24 {
		return FieldError(CodeInvalidInput, "steps", "must contain at most 24 entries")
	}
	if f.State == FormulaApproved || f.State == FormulaRetired {
		if err := ValidateFormulaSteps(f.Steps, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateFormulaSteps(steps []FormulaStep, required bool) error {
	if required && len(steps) == 0 {
		return FieldError(CodeInvalidInput, "steps", "must contain at least one entry")
	}
	if len(steps) > 24 {
		return FieldError(CodeInvalidInput, "steps", "must contain at most 24 entries")
	}
	for index, step := range steps {
		expected := index + 1
		if step.Sequence != expected {
			return FieldError(CodeInvalidInput, "steps",
				"sequence values must be contiguous and begin at 1")
		}
		if strings.TrimSpace(step.Label) == "" {
			return FieldError(CodeInvalidInput, "steps.label", "is required")
		}
		if len([]rune(step.Label)) > 120 {
			return FieldError(CodeInvalidInput, "steps.label",
				"must not exceed 120 characters")
		}
		if step.TemperatureC < 5 || step.TemperatureC > 90 {
			return FieldError(CodeInvalidInput, "steps.temperature_c",
				"must be between 5 and 90 Celsius")
		}
		if step.HoldMinutes < 1 || step.HoldMinutes > 1440 {
			return FieldError(CodeInvalidInput, "steps.hold_minutes",
				"must be between 1 and 1440")
		}
	}
	return nil
}

func (f *Formula) SetSteps(steps []FormulaStep) error {
	if f.State != FormulaDraft {
		return NewError(CodeFormulaState, "only a draft formula can change process steps")
	}
	if err := ValidateFormulaSteps(steps, false); err != nil {
		return err
	}
	f.Steps = append([]FormulaStep(nil), steps...)
	f.UpdatedAt = time.Now().UTC()
	f.Version++
	return nil
}

func (f *Formula) Approve(now time.Time) error {
	if f.State != FormulaDraft {
		return NewError(CodeFormulaState, "only a draft formula can be approved")
	}
	if err := ValidateFormulaSteps(f.Steps, true); err != nil {
		return err
	}
	stamp := now.UTC()
	f.State = FormulaApproved
	f.ApprovedAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

func (f *Formula) Retire(now time.Time) error {
	if f.State != FormulaApproved {
		return NewError(CodeFormulaState, "only an approved formula can be retired")
	}
	stamp := now.UTC()
	f.State = FormulaRetired
	f.RetiredAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

// MarkSuperseded removes an approved revision from the runnable slot when a
// newer revision of the same family is published. The revision stays approved,
// readable, and immutable; it is merely no longer current. Retired revisions
// are never superseded.
func (f *Formula) MarkSuperseded(now time.Time) error {
	if f.State != FormulaApproved {
		return NewError(CodeFormulaState,
			"only an approved formula can be superseded")
	}
	if f.SupersededAt != nil {
		return nil
	}
	stamp := now.UTC()
	f.SupersededAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

// IsRunnable reports whether the revision is an approved, current formula that
// new runs can attach to.
func (f Formula) IsRunnable() bool {
	return f.State == FormulaApproved && f.SupersededAt == nil
}

// NewInitialFormula builds the revision 1 draft for a brand new family.
func NewInitialFormula(
	id, familyID string,
	now time.Time,
	content RevisionContent,
) (Formula, error) {
	steps := []FormulaStep{}
	if content.Steps != nil {
		steps = append([]FormulaStep(nil), (*content.Steps)...)
	}
	formula := Formula{
		ID:                    id,
		FamilyID:              familyID,
		RevisionNumber:        1,
		Name:                  derefString(content.Name),
		Style:                 derefString(content.Style),
		TargetOriginalGravity: derefFloat(content.TargetOriginalGravity),
		TargetFinalGravity:    derefFloat(content.TargetFinalGravity),
		MaxFermentationTempC:  derefFloat(content.MaxFermentationTempC),
		MinimumDays:           derefInt(content.MinimumDays),
		MaximumDays:           derefInt(content.MaximumDays),
		Steps:                 steps,
		State:                 FormulaDraft,
		Version:               1,
		CreatedAt:             now.UTC(),
		UpdatedAt:             now.UTC(),
	}
	if err := formula.Validate(); err != nil {
		return Formula{}, err
	}
	return formula, nil
}

// NewRevisionCandidate builds a controlled revision draft from an approved
// source revision. The candidate inherits the family, name, and lineage, and
// either inherits each content field from the source or applies an override.
// Only an approved revision can serve as the source, and a candidate created
// from a superseded source is rejected by the caller through the family check.
func NewRevisionCandidate(
	id string,
	revisionNumber int,
	source Formula,
	familyID string,
	content RevisionContent,
	now time.Time,
) (Formula, error) {
	if source.State != FormulaApproved {
		return Formula{}, NewError(CodeFormulaRevisionInvalid,
			"a revision can only start from an approved formula revision")
	}
	if source.FamilyID != familyID {
		return Formula{}, NewError(CodeFormulaRevisionInvalid,
			"the source revision does not belong to the given formula family")
	}

	steps := append([]FormulaStep(nil), source.Steps...)
	if content.Steps != nil {
		steps = append([]FormulaStep(nil), (*content.Steps)...)
	}
	name := source.Name
	if content.Name != nil {
		name = *content.Name
	}
	sourceID := source.ID
	sourceRevision := source.RevisionNumber
	candidate := Formula{
		ID:                    id,
		FamilyID:              source.FamilyID,
		RevisionNumber:        revisionNumber,
		RevisionOfID:          &sourceID,
		SourceRevisionNumber:  &sourceRevision,
		Name:                  name,
		Style:                 chooseString(content.Style, source.Style),
		TargetOriginalGravity: chooseFloat(content.TargetOriginalGravity, source.TargetOriginalGravity),
		TargetFinalGravity:    chooseFloat(content.TargetFinalGravity, source.TargetFinalGravity),
		MaxFermentationTempC:  chooseFloat(content.MaxFermentationTempC, source.MaxFermentationTempC),
		MinimumDays:           chooseInt(content.MinimumDays, source.MinimumDays),
		MaximumDays:           chooseInt(content.MaximumDays, source.MaximumDays),
		Steps:                 steps,
		State:                 FormulaDraft,
		Version:               1,
		CreatedAt:             now.UTC(),
		UpdatedAt:             now.UTC(),
	}
	if err := ValidateFormulaSteps(candidate.Steps, true); err != nil {
		return Formula{}, err
	}
	if err := candidate.Validate(); err != nil {
		return Formula{}, err
	}
	return candidate, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func derefInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func chooseString(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func chooseFloat(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}

func chooseInt(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func GravityDifference(original, final float64) float64 {
	return math.Abs(original - final)
}
