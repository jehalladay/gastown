// Package crew provides crew workspace management for overseer workspaces.
package crew

import "time"

// CrewWorker represents a user-managed workspace in a rig.
type CrewWorker struct {
	// Name is the crew worker identifier.
	Name string `json:"name"`

	// Rig is the rig this crew worker belongs to.
	Rig string `json:"rig"`

	// ClonePath is the path to the crew worker's clone of the rig.
	ClonePath string `json:"clone_path"`

	// Branch is the current git branch.
	Branch string `json:"branch"`

	// CreatedAt is when the crew worker was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the crew worker was last updated.
	UpdatedAt time.Time `json:"updated_at"`

	// RemoteNode is the cluster node instance ID this crew's agent runs on when
	// spawned via `gt crew start --remote` (empty = local). Persisted so the agent
	// is inspectable host-side (gt crew status shows the node) without sshing it —
	// the F2 observability requirement.
	RemoteNode string `json:"remote_node,omitempty"`
}

// Summary provides a concise view of crew worker status.
type Summary struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
}

// Summary returns a Summary for this crew worker.
func (c *CrewWorker) Summary() Summary {
	return Summary{
		Name:   c.Name,
		Branch: c.Branch,
	}
}
