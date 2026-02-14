package main

type ActionType string

const (
	ActionCreate ActionType = "create"
	ActionDelete ActionType = "delete"
	ActionWarn   ActionType = "warn"
)

type ReconcileAction struct {
	Type   ActionType
	FQDN   string
	IP     string
	ID     string // Unifi record ID (for deletes)
	Reason string
}

// Reconcile computes the actions needed to bring Unifi DNS records in sync
// with the desired state from Consul. Each desired FQDN should have one A
// record per node IP. It only considers existing records with the managed
// description tag.
func Reconcile(desired []DesiredRecord, existing []DNSRecord, nodeIPs []string) []ReconcileAction {
	// Build desired IP set for quick lookup
	wantIPs := make(map[string]bool, len(nodeIPs))
	for _, ip := range nodeIPs {
		wantIPs[ip] = true
	}

	// Index existing records by FQDN. An FQDN is considered managed if any
	// of its records have a value matching a traefik node IP. All records
	// under a managed FQDN are then treated as managed (including stale IPs
	// from removed nodes).
	managedFQDNs := make(map[string]bool)
	for _, r := range existing {
		if wantIPs[r.Value] {
			managedFQDNs[r.Key] = true
		}
	}

	managed := make(map[string][]DNSRecord)
	unmanaged := make(map[string]bool)
	for _, r := range existing {
		if managedFQDNs[r.Key] {
			managed[r.Key] = append(managed[r.Key], r)
		} else {
			unmanaged[r.Key] = true
		}
	}

	// Build desired FQDN set
	desiredSet := make(map[string]bool)
	var actions []ReconcileAction

	for _, d := range desired {
		desiredSet[d.FQDN] = true

		if recs, ok := managed[d.FQDN]; ok {
			// Find which IPs already have records and which are extra
			haveIPs := make(map[string]bool)
			for _, rec := range recs {
				if wantIPs[rec.Value] && !haveIPs[rec.Value] {
					// Correct IP, first occurrence — keep it
					haveIPs[rec.Value] = true
				} else {
					// Wrong IP or duplicate — delete
					reason := "IP not in node list, removing"
					if wantIPs[rec.Value] {
						reason = "removing duplicate"
					}
					actions = append(actions, ReconcileAction{
						Type:   ActionDelete,
						FQDN:   d.FQDN,
						ID:     rec.ID,
						Reason: reason,
					})
				}
			}

			// Create records for missing IPs
			for _, ip := range nodeIPs {
				if !haveIPs[ip] {
					actions = append(actions, ReconcileAction{
						Type:   ActionCreate,
						FQDN:   d.FQDN,
						IP:     ip,
						Reason: "new record",
					})
				}
			}
		} else if unmanaged[d.FQDN] {
			// Conflicts with a manually created record
			actions = append(actions, ReconcileAction{
				Type:   ActionWarn,
				FQDN:   d.FQDN,
				Reason: "conflicts with unmanaged record, skipping",
			})
		} else {
			// New FQDN — create one record per node IP
			for _, ip := range nodeIPs {
				actions = append(actions, ReconcileAction{
					Type:   ActionCreate,
					FQDN:   d.FQDN,
					IP:     ip,
					Reason: "new record",
				})
			}
		}
	}

	// Delete managed records no longer in desired set
	for fqdn, recs := range managed {
		if !desiredSet[fqdn] {
			for _, rec := range recs {
				actions = append(actions, ReconcileAction{
					Type:   ActionDelete,
					FQDN:   fqdn,
					ID:     rec.ID,
					Reason: "service removed from consul",
				})
			}
		}
	}

	return actions
}
