package main

import (
	"testing"
)

var testNodeIPs = []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}

// findAction searches for a ReconcileAction matching the given type and FQDN.
func findAction(actions []ReconcileAction, typ ActionType, fqdn string) *ReconcileAction {
	for i := range actions {
		if actions[i].Type == typ && actions[i].FQDN == fqdn {
			return &actions[i]
		}
	}
	return nil
}

func findActions(actions []ReconcileAction, typ ActionType, fqdn string) []ReconcileAction {
	var result []ReconcileAction
	for _, a := range actions {
		if a.Type == typ && a.FQDN == fqdn {
			result = append(result, a)
		}
	}
	return result
}

func TestReconcile_NoChanges(t *testing.T) {
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	existing := []DNSRecord{
		{ID: "rec-1", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "web.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
		{ID: "rec-3", Key: "web.home.jpopa.com", Value: "10.0.0.3", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d: %+v", len(actions), actions)
	}
}

func TestReconcile_CreateNew(t *testing.T) {
	desired := []DesiredRecord{{FQDN: "new.home.jpopa.com", ServiceName: "new"}}
	var existing []DNSRecord

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions (one create per node IP), got %d: %+v", len(actions), actions)
	}

	createdIPs := make(map[string]bool)
	for _, a := range actions {
		if a.Type != ActionCreate {
			t.Errorf("expected create, got %q", a.Type)
		}
		if a.FQDN != "new.home.jpopa.com" {
			t.Errorf("FQDN = %q, want %q", a.FQDN, "new.home.jpopa.com")
		}
		createdIPs[a.IP] = true
	}
	for _, ip := range testNodeIPs {
		if !createdIPs[ip] {
			t.Errorf("missing create for IP %s", ip)
		}
	}
}

func TestReconcile_DeleteOrphan(t *testing.T) {
	var desired []DesiredRecord
	existing := []DNSRecord{
		{ID: "rec-1", Key: "old.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "old.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 2 {
		t.Fatalf("expected 2 delete actions, got %d: %+v", len(actions), actions)
	}
	for _, a := range actions {
		if a.Type != ActionDelete {
			t.Errorf("expected delete, got %q", a.Type)
		}
	}
}

func TestReconcile_AddMissingIPs(t *testing.T) {
	// FQDN exists with one IP, needs two more
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	existing := []DNSRecord{
		{ID: "rec-1", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)
	creates := findActions(actions, ActionCreate, "web.home.jpopa.com")
	if len(creates) != 2 {
		t.Fatalf("expected 2 creates for missing IPs, got %d: %+v", len(creates), creates)
	}

	createdIPs := make(map[string]bool)
	for _, a := range creates {
		createdIPs[a.IP] = true
	}
	if createdIPs["10.0.0.1"] {
		t.Error("should not re-create existing IP 10.0.0.1")
	}
	if !createdIPs["10.0.0.2"] || !createdIPs["10.0.0.3"] {
		t.Error("should create records for 10.0.0.2 and 10.0.0.3")
	}
}

func TestReconcile_RemoveStaleIP(t *testing.T) {
	// FQDN has a record with an IP not in the node list
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	existing := []DNSRecord{
		{ID: "rec-1", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "web.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
		{ID: "rec-3", Key: "web.home.jpopa.com", Value: "10.0.0.3", Description: managedDescription},
		{ID: "rec-stale", Key: "web.home.jpopa.com", Value: "10.0.0.99", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action (delete stale), got %d: %+v", len(actions), actions)
	}
	if actions[0].Type != ActionDelete || actions[0].ID != "rec-stale" {
		t.Errorf("expected delete of rec-stale, got %+v", actions[0])
	}
}

func TestReconcile_UnmanagedConflict(t *testing.T) {
	desired := []DesiredRecord{{FQDN: "manual.home.jpopa.com", ServiceName: "svc"}}
	existing := []DNSRecord{{
		ID:          "rec-manual",
		Key:         "manual.home.jpopa.com",
		Value:       "10.0.0.50",
		Description: "user-created",
	}}

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(actions), actions)
	}
	if actions[0].Type != ActionWarn {
		t.Errorf("Type = %q, want %q", actions[0].Type, ActionWarn)
	}
}

func TestReconcile_MixedScenario(t *testing.T) {
	desired := []DesiredRecord{
		{FQDN: "keep.home.jpopa.com", ServiceName: "keep"},
		{FQDN: "new.home.jpopa.com", ServiceName: "new"},
	}
	existing := []DNSRecord{
		{ID: "rec-k1", Key: "keep.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-k2", Key: "keep.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
		{ID: "rec-k3", Key: "keep.home.jpopa.com", Value: "10.0.0.3", Description: managedDescription},
		{ID: "rec-del", Key: "stale.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)

	creates := findActions(actions, ActionCreate, "new.home.jpopa.com")
	if len(creates) != 3 {
		t.Errorf("expected 3 creates for new.home.jpopa.com, got %d", len(creates))
	}

	del := findAction(actions, ActionDelete, "stale.home.jpopa.com")
	if del == nil {
		t.Error("expected delete action for stale.home.jpopa.com")
	}

	// "keep" should produce no actions
	keepActions := findActions(actions, ActionCreate, "keep.home.jpopa.com")
	keepDeletes := findActions(actions, ActionDelete, "keep.home.jpopa.com")
	if len(keepActions) != 0 || len(keepDeletes) != 0 {
		t.Error("expected no actions for keep.home.jpopa.com")
	}

	// Total: 3 creates + 1 delete = 4
	if len(actions) != 4 {
		t.Errorf("expected 4 actions, got %d: %+v", len(actions), actions)
	}
}

func TestReconcile_BothEmpty(t *testing.T) {
	actions := Reconcile(nil, nil, testNodeIPs)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}

	actions = Reconcile([]DesiredRecord{}, []DNSRecord{}, testNodeIPs)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for empty slices, got %d", len(actions))
	}
}

func TestReconcile_DuplicateManagedRecords(t *testing.T) {
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	existing := []DNSRecord{
		{ID: "rec-1", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-1-dup", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "web.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)

	// Should delete the duplicate and create the missing IP
	deletes := findActions(actions, ActionDelete, "web.home.jpopa.com")
	if len(deletes) != 1 || deletes[0].ID != "rec-1-dup" {
		t.Errorf("expected 1 delete of rec-1-dup, got %+v", deletes)
	}

	creates := findActions(actions, ActionCreate, "web.home.jpopa.com")
	if len(creates) != 1 || creates[0].IP != "10.0.0.3" {
		t.Errorf("expected 1 create for 10.0.0.3, got %+v", creates)
	}
}

func TestReconcile_DuplicateOrphansBothDeleted(t *testing.T) {
	var desired []DesiredRecord
	existing := []DNSRecord{
		{ID: "rec-1", Key: "old.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "old.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, testNodeIPs)

	deletedIDs := make(map[string]bool)
	for _, a := range actions {
		if a.Type != ActionDelete {
			t.Errorf("expected only deletes, got %+v", a)
		}
		deletedIDs[a.ID] = true
	}
	if !deletedIDs["rec-1"] || !deletedIDs["rec-2"] {
		t.Errorf("expected both orphan duplicates deleted, got %v", deletedIDs)
	}
}

func TestReconcile_UnmanagedIgnoredInOrphanPhase(t *testing.T) {
	var desired []DesiredRecord
	existing := []DNSRecord{{
		ID:          "rec-unmanaged",
		Key:         "manual.home.jpopa.com",
		Value:       "10.0.0.50",
		Description: "user-created",
	}}

	actions := Reconcile(desired, existing, testNodeIPs)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions (unmanaged record should not be deleted), got %d: %+v", len(actions), actions)
	}
}

func TestReconcile_SingleNodeIP(t *testing.T) {
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	var existing []DNSRecord

	actions := Reconcile(desired, existing, []string{"10.0.0.1"})
	if len(actions) != 1 {
		t.Fatalf("expected 1 create, got %d: %+v", len(actions), actions)
	}
	if actions[0].Type != ActionCreate || actions[0].IP != "10.0.0.1" {
		t.Errorf("unexpected action: %+v", actions[0])
	}
}

func TestReconcile_NodeRemoved(t *testing.T) {
	// Had 3 nodes, now only 2 — record for removed node should be deleted
	desired := []DesiredRecord{{FQDN: "web.home.jpopa.com", ServiceName: "web"}}
	existing := []DNSRecord{
		{ID: "rec-1", Key: "web.home.jpopa.com", Value: "10.0.0.1", Description: managedDescription},
		{ID: "rec-2", Key: "web.home.jpopa.com", Value: "10.0.0.2", Description: managedDescription},
		{ID: "rec-3", Key: "web.home.jpopa.com", Value: "10.0.0.3", Description: managedDescription},
	}

	actions := Reconcile(desired, existing, []string{"10.0.0.1", "10.0.0.2"})
	if len(actions) != 1 {
		t.Fatalf("expected 1 action (delete removed node), got %d: %+v", len(actions), actions)
	}
	if actions[0].Type != ActionDelete || actions[0].ID != "rec-3" {
		t.Errorf("expected delete of rec-3, got %+v", actions[0])
	}
}
