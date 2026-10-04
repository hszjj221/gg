package app

import (
	"errors"
	"fmt"

	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/library"
)

type publishSource interface {
	Get(string) (*artifact.Artifact, error)
	ReadVersion(string, int) (string, error)
	Publish(string, int) (string, int, error)
}

type publishCollection interface {
	AddBytes(string, []byte, string) (*library.Entry, error)
	Remove(string) error
}

// PublishArtifact saves the selected version to the library before marking it
// published. CLI and daemon use the same version check and compensation.
func PublishArtifact(store publishSource, collection publishCollection, id string) (PublishResult, error) {
	a, err := store.Get(id)
	if err != nil {
		return PublishResult{}, err
	}
	content, err := store.ReadVersion(id, a.Version)
	if err != nil {
		return PublishResult{}, err
	}
	entry, err := collection.AddBytes(library.ArtifactFileName(a.Title, a.Type), []byte(content), "artifact:"+a.ID)
	if err != nil {
		return PublishResult{}, fmt.Errorf("save to library: %w", err)
	}
	_, version, err := store.Publish(id, a.Version)
	if err != nil {
		if errors.Is(err, artifact.ErrVersionChanged) {
			err = fmt.Errorf("artifact %q changed during publish, please retry: %w", id, err)
		}
		if cleanupErr := collection.Remove(entry.ID); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove unpublished library copy %q: %w", entry.ID, cleanupErr))
		}
		return PublishResult{}, err
	}
	return PublishResult{ID: id, PublishedVersion: version, LibraryName: entry.Name}, nil
}
