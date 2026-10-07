package workflowcheck

import (
	"fmt"

	"github.com/1260126-cc/solo-0016-cellar-run-control/internal/domain"
)

// formulaRevision exercises the controlled revision flow end to end:
//
//   - a revision candidate starts from the current approved revision and keeps
//     its source lineage;
//   - until the candidate is approved it never replaces the runnable version;
//   - approval publishes the new revision and marks the old one superseded but
//     still readable and immutable;
//   - runs trace the exact formula revision they adopted;
//   - repeated approval, stale sources, and illegal lineage requests fail
//     without leaving partial drafts.
func formulaRevision(api *client) error {
	v1, err := ensureApprovedFormula(api, "Revision Ale")
	if err != nil {
		return err
	}
	if v1.RevisionNumber != 1 || v1.FamilyID == "" {
		return errInvalidWorkflow("approved initial formula has no revision lineage")
	}

	vessel, err := createCheckVessel(api, "VSL-303", 400)
	if err != nil {
		return err
	}
	firstRun, err := createStartedRun(api, v1.ID, vessel.ID, "RUN-REV-FIRST")
	if err != nil {
		return err
	}
	if firstRun.Run.FormulaID != v1.ID ||
		firstRun.Run.FormulaRevisionNumber != 1 ||
		firstRun.Run.FormulaFamilyID != v1.FamilyID ||
		firstRun.Run.FormulaName != "Revision Ale" {
		return errInvalidWorkflow("run did not snapshot the adopted formula revision")
	}

	// While the first run is open, revision 1 cannot be retired, but it can be
	// revised.
	retireBlocked, err := api.call("POST", "/v1/formulas/"+v1.ID+"/retire", nil, ptrInt64(v1.Version))
	if err != nil {
		return err
	}
	if err := requireCode(retireBlocked, 409, string(domain.CodeFormulaInUse),
		"retire revision with an open run"); err != nil {
		return err
	}

	// A stale expected version must not open a revision.
	stale, err := api.call("POST", "/v1/formulas/"+v1.ID+"/revisions", nil, ptrInt64(v1.Version-1))
	if err != nil {
		return err
	}
	if err := requireCode(stale, 412, string(domain.CodeRevisionConflict),
		"start revision with stale expected version"); err != nil {
		return err
	}

	// Drafts are not legal revision sources.
	fromDraft, err := api.call("POST", "/v1/formulas", createFormulaPayload("Draft Only"), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(fromDraft, 201, "create draft-only formula"); err != nil {
		return err
	}
	var draftOnly formulaView
	if err := fromDraft.decode(&draftOnly); err != nil {
		return err
	}
	illegalSource, err := api.call("POST", "/v1/formulas/"+draftOnly.ID+"/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(illegalSource, 409, string(domain.CodeFormulaRevisionInvalid),
		"start revision from a draft"); err != nil {
		return err
	}
	missing, err := api.call("POST", "/v1/formulas/formula_doesnotexist/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(missing, 404, string(domain.CodeFormulaNotFound),
		"start revision from a missing formula"); err != nil {
		return err
	}

	candidateResponse, err := api.call("POST", "/v1/formulas/"+v1.ID+"/revisions",
		map[string]any{
			"max_fermentation_temp_c": 20.0,
			"minimum_days":            5,
			"maximum_days":            18,
		}, ptrInt64(v1.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(candidateResponse, 201, "start revision candidate"); err != nil {
		return err
	}
	var v2Draft formulaView
	if err := candidateResponse.decode(&v2Draft); err != nil {
		return err
	}
	if v2Draft.State != domain.FormulaDraft ||
		v2Draft.RevisionNumber != 2 ||
		v2Draft.FamilyID != v1.FamilyID ||
		v2Draft.RevisionOfID == nil || *v2Draft.RevisionOfID != v1.ID ||
		v2Draft.SourceRevisionNumber == nil || *v2Draft.SourceRevisionNumber != 1 {
		return errInvalidWorkflow("revision candidate has incorrect lineage")
	}
	if len(v2Draft.Steps) != 3 {
		return errInvalidWorkflow("revision candidate did not inherit source steps")
	}

	// A second candidate while one is open is rejected; no extra draft exists.
	secondCandidate, err := api.call("POST", "/v1/formulas/"+v1.ID+"/revisions", nil, ptrInt64(v1.Version))
	if err != nil {
		return err
	}
	if err := requireCode(secondCandidate, 409, string(domain.CodeFormulaRevisionOpen),
		"start a second open revision"); err != nil {
		return err
	}

	// Candidate steps can be edited while the draft is open.
	rename, err := api.call("PUT", "/v1/formulas/"+v2Draft.ID+"/steps",
		formulaStepPayload(), ptrInt64(v2Draft.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(rename, 200, "edit candidate steps"); err != nil {
		return err
	}
	var editedDraft formulaView
	if err := rename.decode(&editedDraft); err != nil {
		return err
	}

	// While the candidate is pending, runs still adopt revision 1: the draft has
	// not replaced the current version.
	pendingRunResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-PENDING", v1.ID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(pendingRunResponse, 201, "create run while revision pending"); err != nil {
		return err
	}
	var pendingRun runView
	if err := pendingRunResponse.decode(&pendingRun); err != nil {
		return err
	}
	if pendingRun.FormulaID != v1.ID || pendingRun.FormulaRevisionNumber != 1 {
		return errInvalidWorkflow("pending revision replaced the runnable version")
	}
	draftRun, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-DRAFT", v2Draft.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(draftRun, 409, string(domain.CodeFormulaState),
		"create run against a draft revision"); err != nil {
		return err
	}

	editedResponse, err := api.call("PUT", "/v1/formulas/"+v2Draft.ID+"/steps",
		revisedStepPayload(), ptrInt64(editedDraft.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(editedResponse, 200, "replace candidate steps"); err != nil {
		return err
	}
	var edited formulaView
	if err := editedResponse.decode(&edited); err != nil {
		return err
	}

	approvedResponse, err := api.call("POST", "/v1/formulas/"+v2Draft.ID+"/approve",
		nil, ptrInt64(edited.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(approvedResponse, 200, "approve revision candidate"); err != nil {
		return err
	}
	var v2 formulaView
	if err := approvedResponse.decode(&v2); err != nil {
		return err
	}
	if v2.State != domain.FormulaApproved || v2.RevisionNumber != 2 {
		return errInvalidWorkflow("candidate was not published as revision 2")
	}

	// Repeated approval is rejected against either the stale guard or state.
	repeat, err := api.call("POST", "/v1/formulas/"+v2.ID+"/approve",
		nil, ptrInt64(v2.Version))
	if err != nil {
		return err
	}
	if err := requireCode(repeat, 409, string(domain.CodeFormulaState),
		"approve revision twice"); err != nil {
		return err
	}

	// The old revision is superseded: readable, immutable, and not runnable.
	oldResponse, err := api.call("GET", "/v1/formulas/"+v1.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(oldResponse, 200, "read superseded revision"); err != nil {
		return err
	}
	var superseded formulaView
	if err := oldResponse.decode(&superseded); err != nil {
		return err
	}
	if superseded.State != domain.FormulaApproved || superseded.SupersededAt == nil {
		return errInvalidWorkflow("old revision was not marked superseded")
	}
	staleRun, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-STALE", v1.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(staleRun, 409, string(domain.CodeFormulaVersionStale),
		"create run against a superseded revision"); err != nil {
		return err
	}
	reviseStale, err := api.call("POST", "/v1/formulas/"+v1.ID+"/revisions",
		nil, ptrInt64(superseded.Version))
	if err != nil {
		return err
	}
	if err := requireCode(reviseStale, 409, string(domain.CodeFormulaVersionStale),
		"revise a superseded revision"); err != nil {
		return err
	}
	mutateStale, err := api.call("PUT", "/v1/formulas/"+v1.ID+"/steps",
		formulaStepPayload(), ptrInt64(superseded.Version))
	if err != nil {
		return err
	}
	if err := requireCode(mutateStale, 409, string(domain.CodeFormulaState),
		"edit a superseded revision"); err != nil {
		return err
	}

	// New runs adopt revision 2 and keep the lineage.
	newRunResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-SECOND", v2.ID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(newRunResponse, 201, "create run on new revision"); err != nil {
		return err
	}
	var newRun runView
	if err := newRunResponse.decode(&newRun); err != nil {
		return err
	}
	if newRun.FormulaID != v2.ID || newRun.FormulaRevisionNumber != 2 {
		return errInvalidWorkflow("new run did not adopt revision 2")
	}

	// A revision 3 can chain from the new current revision and override values.
	badName, err := api.call("POST", "/v1/formulas/"+v2.ID+"/revisions",
		map[string]any{"name": "A Totally Different Ale"}, ptrInt64(v2.Version))
	if err != nil {
		return err
	}
	if err := requireCode(badName, 400, string(domain.CodeInvalidInput),
		"rename a revision family"); err != nil {
		return err
	}
	invalidOverride, err := api.call("POST", "/v1/formulas/"+v2.ID+"/revisions",
		map[string]any{"maximum_days": 999}, ptrInt64(v2.Version))
	if err != nil {
		return err
	}
	if err := requireCode(invalidOverride, 400, string(domain.CodeInvalidInput),
		"start revision with invalid overrides"); err != nil {
		return err
	}
	v3Response, err := api.call("POST", "/v1/formulas/"+v2.ID+"/revisions",
		map[string]any{"target_final_gravity": 1.010}, ptrInt64(v2.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(v3Response, 201, "chain revision 3"); err != nil {
		return err
	}
	var v3Draft formulaView
	if err := v3Response.decode(&v3Draft); err != nil {
		return err
	}
	if v3Draft.RevisionNumber != 3 ||
		v3Draft.RevisionOfID == nil || *v3Draft.RevisionOfID != v2.ID ||
		v3Draft.SourceRevisionNumber == nil || *v3Draft.SourceRevisionNumber != 2 {
		return errInvalidWorkflow("chained revision has incorrect source")
	}
	v3Approve, err := api.call("POST", "/v1/formulas/"+v3Draft.ID+"/approve",
		nil, ptrInt64(v3Draft.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(v3Approve, 200, "approve revision 3"); err != nil {
		return err
	}

	// Listing exposes the whole lineage ordered by revision.
	listResponse, err := api.call("GET", "/v1/formulas", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(listResponse, 200, "list formula lineage"); err != nil {
		return err
	}
	var list struct {
		Formulas []formulaView `json:"formulas"`
	}
	if err := listResponse.decode(&list); err != nil {
		return err
	}
	var lineage []formulaView
	for _, formula := range list.Formulas {
		if formula.FamilyID == v1.FamilyID {
			lineage = append(lineage, formula)
		}
	}
	if len(lineage) != 3 ||
		lineage[0].RevisionNumber != 1 ||
		lineage[1].RevisionNumber != 2 ||
		lineage[2].RevisionNumber != 3 {
		return fmt.Errorf("formula lineage was not listed in revision order")
	}
	return nil
}

func revisedStepPayload() map[string]any {
	return map[string]any{
		"steps": []map[string]any{
			{
				"sequence":      1,
				"label":         "Warm and hydrate",
				"temperature_c": 64.0,
				"hold_minutes":  45,
			},
			{
				"sequence":      2,
				"label":         "Cool to pitch",
				"temperature_c": 18.0,
				"hold_minutes":  40,
			},
		},
	}
}
