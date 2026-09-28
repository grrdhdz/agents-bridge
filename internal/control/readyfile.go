package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ReadyFile is a --ready-file path (§7.1) that must appear already complete:
// whoever launched the bridge polls for it, so an empty or half-written file
// would hand them a missing instance_id. Reserve checks the path up front, so
// a stale file is reported before anything starts; Publish writes the record
// to a private temporary file beside it and hard-links that into place, which
// is atomic and, like O_EXCL, never replaces a file that already exists.
type ReadyFile struct {
	path string
}

// ReserveReadyFile fails if path already exists. It creates nothing: the file
// only appears once Publish has its full content.
func ReserveReadyFile(path string) (*ReadyFile, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrExist}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &ReadyFile{path: path}, nil
}

// Publish makes path appear with exactly data, owner-only (§5.1). It fails
// with os.ErrExist if something created path since Reserve.
func (r *ReadyFile) Publish(data []byte) error {
	suffix, err := randomCapability()
	if err != nil {
		return err
	}
	tmpName := filepath.Join(filepath.Dir(r.path), "."+filepath.Base(r.path)+"."+suffix[:16]+".tmp")
	tmp, err := CreatePrivateFile(tmpName)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	linkErr := os.Link(tmpName, r.path)
	if linkErr == nil {
		return nil
	}
	if errors.Is(linkErr, os.ErrExist) {
		return &os.PathError{Op: "open", Path: r.path, Err: os.ErrExist}
	}
	// A filesystem without hard links (FAT, some network shares): fall back
	// to an exclusive create. The file is briefly empty there, but still
	// owner-only and never overwrites an existing one.
	file, err := CreatePrivateFile(r.path)
	if err != nil {
		return fmt.Errorf("%w (hard link: %v)", err, linkErr)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
