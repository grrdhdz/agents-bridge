package control

import (
	"errors"
	"path/filepath"

	"github.com/grrdhdz/agents-bridge/engine/internal/protocol"
)

// RuntimeRoot is the namespace shared by descriptors and hook metadata.
// Resolving it does not create files or contact any bridge.
func RuntimeRoot() (string, error) {
	root, err := platformDescriptorRoot()
	return filepath.Dir(root), err
}

// EnsurePrivateDir reuses descriptor ownership checks and Windows ACLs.
func EnsurePrivateDir(root string) error {
	if root == "" {
		return errors.New("private directory requires a path")
	}
	_, err := descriptorDir(root)
	return err
}

var ErrInstanceNotFound = errors.New("INSTANCE_NOT_FOUND")

// FindDescriptor performs local discovery without stale network probes. Hooks
// then check only the selected endpoint under their total time budget.
func FindDescriptor(root, instanceID string, role protocol.Role) (Descriptor, error) {
	ds, err := listDescriptors(root)
	if err != nil {
		return Descriptor{}, err
	}
	for _, d := range ds {
		if d.InstanceID == instanceID && d.LocalRole == role {
			return d, nil
		}
	}
	return Descriptor{}, ErrInstanceNotFound
}
