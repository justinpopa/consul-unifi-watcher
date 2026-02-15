package main

import "strings"

const OwnerValue = "consul-dns-watcher"

type ActionType string

const (
	ActionCreate ActionType = "create"
	ActionDelete ActionType = "delete"
	ActionWarn   ActionType = "warn"
)

type ReconcileAction struct {
	Type       ActionType
	FQDN       string
	IP         string
	RecordType string // "A" or "TXT"
	Value      string // IP for A records, OwnerValue for TXT records
	ID         string // Unifi record ID (for deletes)
	Reason     string
}

// txtKey returns the TXT ownership marker key for an FQDN.
func txtKey(fqdn string) string {
	return "_managed." + fqdn
}

// Reconcile computes the actions needed to bring Unifi DNS records in sync
// with the desired state from Consul. Ownership is determined by the presence
// of a companion TXT record (_managed.<fqdn> = "consul-dns-watcher").
func Reconcile(desired []DesiredRecord, existing []DNSRecord, nodeIPs []string) []ReconcileAction {
	// Build desired IP set for quick lookup
	wantIPs := make(map[string]bool, len(nodeIPs))
	for _, ip := range nodeIPs {
		wantIPs[ip] = true
	}

	// Scan existing records to find TXT ownership markers.
	// An FQDN is managed if _managed.<fqdn> TXT record exists with OwnerValue.
	managedFQDNs := make(map[string]bool)
	txtRecords := make(map[string]DNSRecord) // fqdn -> TXT record
	for _, r := range existing {
		if r.RecordType == "TXT" && r.Value == OwnerValue && strings.HasPrefix(r.Key, "_managed.") {
			fqdn := strings.TrimPrefix(r.Key, "_managed.")
			managedFQDNs[fqdn] = true
			txtRecords[fqdn] = r
		}
	}

	// Index existing A records by FQDN into managed/unmanaged buckets.
	managed := make(map[string][]DNSRecord)   // managed A records
	unmanaged := make(map[string]bool)         // FQDNs with A records but no TXT marker
	for _, r := range existing {
		if r.RecordType == "TXT" {
			continue
		}
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

		if managedFQDNs[d.FQDN] {
			// Managed FQDN — reconcile A records
			recs := managed[d.FQDN]
			haveIPs := make(map[string]bool)
			for _, rec := range recs {
				if wantIPs[rec.Value] && !haveIPs[rec.Value] {
					haveIPs[rec.Value] = true
				} else {
					reason := "IP not in node list, removing"
					if wantIPs[rec.Value] {
						reason = "removing duplicate"
					}
					actions = append(actions, ReconcileAction{
						Type:       ActionDelete,
						FQDN:       d.FQDN,
						ID:         rec.ID,
						RecordType: "A",
						Reason:     reason,
					})
				}
			}

			// Create records for missing IPs
			for _, ip := range nodeIPs {
				if !haveIPs[ip] {
					actions = append(actions, ReconcileAction{
						Type:       ActionCreate,
						FQDN:       d.FQDN,
						IP:         ip,
						RecordType: "A",
						Value:      ip,
						Reason:     "new record",
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
			// New FQDN — create TXT marker + one A record per node IP
			actions = append(actions, ReconcileAction{
				Type:       ActionCreate,
				FQDN:       d.FQDN,
				RecordType: "TXT",
				Value:      OwnerValue,
				Reason:     "ownership marker",
			})
			for _, ip := range nodeIPs {
				actions = append(actions, ReconcileAction{
					Type:       ActionCreate,
					FQDN:       d.FQDN,
					IP:         ip,
					RecordType: "A",
					Value:      ip,
					Reason:     "new record",
				})
			}
		}
	}

	// Delete managed records no longer in desired set (A records + TXT marker)
	for fqdn := range managedFQDNs {
		if !desiredSet[fqdn] {
			// Delete A records
			for _, rec := range managed[fqdn] {
				actions = append(actions, ReconcileAction{
					Type:       ActionDelete,
					FQDN:       fqdn,
					ID:         rec.ID,
					RecordType: "A",
					Reason:     "service removed from consul",
				})
			}
			// Delete TXT marker
			if txt, ok := txtRecords[fqdn]; ok {
				actions = append(actions, ReconcileAction{
					Type:       ActionDelete,
					FQDN:       fqdn,
					ID:         txt.ID,
					RecordType: "TXT",
					Reason:     "service removed from consul",
				})
			}
		}
	}

	return actions
}
