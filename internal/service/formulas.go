package service

import (
	"sort"

	"github.com/1260126-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateFormula(input CreateFormulaInput) (domain.Formula, error) {
	now := s.clock.Now()
	name := domain.NormalizeName(input.Name)
	content := domain.RevisionContent{
		Name:                  &name,
		Style:                 stringPointer(domain.NormalizeName(input.Style)),
		TargetOriginalGravity: floatPointer(input.TargetOriginalGravity),
		TargetFinalGravity:    floatPointer(input.TargetFinalGravity),
		MaxFermentationTempC:  floatPointer(input.MaxFermentationTempC),
		MinimumDays:           intPointer(input.MinimumDays),
		MaximumDays:           intPointer(input.MaximumDays),
	}
	id, err := generateEntityID("formula")
	if err != nil {
		return domain.Formula{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	familyID, err := generateEntityID("fam")
	if err != nil {
		return domain.Formula{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	formula, err := domain.NewInitialFormula(id, familyID, now, content)
	if err != nil {
		return domain.Formula{}, err
	}
	nameKey := domain.NameKey(formula.Name)
	draftID := id
	family := domain.FormulaFamily{
		ID:           familyID,
		Name:         formula.Name,
		DraftID:      &draftID,
		NextRevision: 2,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, exists := snapshot.NameIndex[nameKey]; exists {
			return domain.NewError(domain.CodeFormulaNameConflict,
				"a formula with the same normalized name already exists")
		}
		snapshot.FormulaFamilies[familyID] = family
		snapshot.Formulas[id] = formula
		snapshot.NameIndex[nameKey] = familyID
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return formula.Clone(), nil
}

// StartFormulaRevision opens a controlled revision candidate from the current
// approved revision of a formula family. The candidate is a new draft; the
// source revision stays current, immutable, and runnable until the candidate is
// approved. Every precondition is checked before any state is touched, so a
// rejected request never leaves a partial draft.
func (s *Service) StartFormulaRevision(
	id string,
	input StartFormulaRevisionInput,
) (domain.Formula, error) {
	now := s.clock.Now()
	candidateID, err := generateEntityID("formula")
	if err != nil {
		return domain.Formula{}, domain.NewError(domain.CodeStorage, err.Error())
	}

	var candidate domain.Formula
	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		source, ok := snapshot.Formulas[id]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if err := s.checkExpected(source.Version, input.ExpectedVersion); err != nil {
			return err
		}
		family, ok := snapshot.FormulaFamilies[source.FamilyID]
		if !ok {
			return domain.NewError(domain.CodeFormulaRevisionInvalid,
				"formula has no revision family")
		}
		if source.State != domain.FormulaApproved {
			return domain.NewError(domain.CodeFormulaRevisionInvalid,
				"a revision can only start from an approved formula revision")
		}
		if !family.IsCurrent(source.ID) {
			return domain.NewError(domain.CodeFormulaVersionStale,
				"a revision can only start from the current approved version")
		}
		if family.HasDraft() {
			return domain.NewError(domain.CodeFormulaRevisionOpen,
				"finish or approve the open draft revision before starting another one")
		}
		content := domain.RevisionContent{
			Name:                  input.Name,
			Style:                 input.Style,
			TargetOriginalGravity: input.TargetOriginalGravity,
			TargetFinalGravity:    input.TargetFinalGravity,
			MaxFermentationTempC:  input.MaxFermentationTempC,
			MinimumDays:           input.MinimumDays,
			MaximumDays:           input.MaximumDays,
			Steps:                 input.Steps,
		}
		if content.Name != nil {
			normalized := domain.NormalizeName(*content.Name)
			content.Name = &normalized
			if normalized != family.Name {
				return domain.FieldError(domain.CodeInvalidInput, "name",
					"a revision keeps the formula family name")
			}
		}
		if content.Style != nil {
			normalized := domain.NormalizeName(*content.Style)
			content.Style = &normalized
		}
		draft, buildErr := domain.NewRevisionCandidate(
			candidateID, family.NextRevision, source, family.ID, content, now)
		if buildErr != nil {
			return buildErr
		}
		if err := family.OpenDraft(draft.ID, now); err != nil {
			return err
		}
		snapshot.FormulaFamilies[family.ID] = family
		snapshot.Formulas[draft.ID] = draft
		candidate = draft.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return candidate, nil
}

func (s *Service) SetFormulaSteps(
	id string,
	input SetFormulaStepsInput,
) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		formula, ok := snapshot.Formulas[id]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := formula.SetSteps(input.Steps); err != nil {
			return err
		}
		formula.UpdatedAt = s.clock.Now()
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

// ApproveFormula approves a draft revision. For revision 1 the draft becomes
// the family's first runnable version. For later revision candidates the
// previous current version is marked superseded atomically; it remains
// readable but is never replaced or mutated in content. Repeating approval on
// an approved revision is rejected.
func (s *Service) ApproveFormula(id string, input VersionInput) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		formula, ok := snapshot.Formulas[id]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		family, ok := snapshot.FormulaFamilies[formula.FamilyID]
		if !ok {
			return domain.NewError(domain.CodeFormulaRevisionInvalid,
				"formula has no revision family")
		}
		if formula.State == domain.FormulaApproved {
			return domain.NewError(domain.CodeFormulaState,
				"formula revision has already been approved")
		}
		if formula.State != domain.FormulaDraft {
			return domain.NewError(domain.CodeFormulaState,
				"only a draft formula can be approved")
		}
		if family.DraftID == nil || *family.DraftID != id {
			return domain.NewError(domain.CodeFormulaRevisionInvalid,
				"the draft revision is not the open candidate of its family")
		}
		now := s.clock.Now()
		if err := formula.Approve(now); err != nil {
			return err
		}
		previousID := family.Publish(formula.ID, now)
		if previousID != nil {
			previous, found := snapshot.Formulas[*previousID]
			if !found {
				return domain.NewError(domain.CodeFormulaRevisionInvalid,
					"previous current formula revision was not found")
			}
			if err := previous.MarkSuperseded(now); err != nil {
				return err
			}
			snapshot.Formulas[previous.ID] = previous
		}
		snapshot.FormulaFamilies[family.ID] = family
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

func (s *Service) RetireFormula(id string, input VersionInput) (domain.Formula, error) {
	var updated domain.Formula
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		formula, ok := snapshot.Formulas[id]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if err := s.checkExpected(formula.Version, input.ExpectedVersion); err != nil {
			return err
		}
		for _, run := range snapshot.Runs {
			if run.FormulaID == id && run.State != domain.RunCompleted &&
				run.State != domain.RunAborted {
				return domain.NewError(domain.CodeFormulaInUse,
					"formula is attached to a run that is still open")
			}
		}
		family, ok := snapshot.FormulaFamilies[formula.FamilyID]
		if !ok {
			return domain.NewError(domain.CodeFormulaRevisionInvalid,
				"formula has no revision family")
		}
		if err := formula.Retire(s.clock.Now()); err != nil {
			return err
		}
		if family.CurrentID != nil && *family.CurrentID == id {
			family.CurrentID = nil
			family.UpdatedAt = s.clock.Now()
		}
		snapshot.FormulaFamilies[family.ID] = family
		snapshot.Formulas[id] = formula
		updated = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return updated, nil
}

func (s *Service) GetFormula(id string) (domain.Formula, error) {
	var found domain.Formula
	err := s.store.View(func(snapshot domain.Snapshot) error {
		formula, ok := snapshot.Formulas[id]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		found = formula.Clone()
		return nil
	})
	if err != nil {
		return domain.Formula{}, err
	}
	return found, nil
}

func (s *Service) ListFormulas() ([]domain.Formula, error) {
	var formulas []domain.Formula
	err := s.store.View(func(snapshot domain.Snapshot) error {
		formulas = make([]domain.Formula, 0, len(snapshot.Formulas))
		for _, formula := range snapshot.Formulas {
			formulas = append(formulas, formula.Clone())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(formulas, func(i, j int) bool {
		if formulas[i].Name == formulas[j].Name {
			if formulas[i].RevisionNumber == formulas[j].RevisionNumber {
				return formulas[i].ID < formulas[j].ID
			}
			return formulas[i].RevisionNumber < formulas[j].RevisionNumber
		}
		return formulas[i].Name < formulas[j].Name
	})
	return formulas, nil
}

func stringPointer(value string) *string {
	return &value
}

func floatPointer(value float64) *float64 {
	return &value
}

func intPointer(value int) *int {
	return &value
}
