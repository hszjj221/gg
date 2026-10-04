package app

import (
	"errors"
	"fmt"

	"github.com/hszjj221/gg/internal/artifact"
)

// ArtifactView is the Web-facing shape of one artifact: metadata plus the
// content the user should read (always the latest version, so a draft added
// after publishing stays visible).
type ArtifactView struct {
	Meta             *artifact.Artifact `json:"meta"`
	Content          string             `json:"content"`
	Version          int                `json:"version"`
	PublishedVersion int                `json:"publishedVersion"`
}

// PublishResult reports a publish: the version published and the library
// entry it was saved as.
type PublishResult struct {
	ID               string `json:"id"`
	PublishedVersion int    `json:"publishedVersion"`
	LibraryName      string `json:"libraryName"`
}

func (w *Runtime) artifactStore() (*artifact.Store, error) {
	if w.artifacts == nil {
		return nil, fmt.Errorf("artifact store is not available")
	}
	return w.artifacts, nil
}

// ListArtifacts returns all artifacts, most recently updated first.
func (w *Runtime) ListArtifacts() ([]*artifact.Artifact, error) {
	store, err := w.artifactStore()
	if err != nil {
		return nil, err
	}
	return store.List()
}

// GetArtifact returns the artifact view for id.
func (w *Runtime) GetArtifact(id string) (ArtifactView, error) {
	store, err := w.artifactStore()
	if err != nil {
		return ArtifactView{}, err
	}
	a, err := store.Get(id)
	if err != nil {
		return ArtifactView{}, w.wrapArtifactError(err, id)
	}
	content, err := store.ReadVersion(id, a.Version)
	if err != nil {
		return ArtifactView{}, err
	}
	return ArtifactView{
		Meta:             a,
		Content:          content,
		Version:          a.Version,
		PublishedVersion: a.PublishedVersion,
	}, nil
}

// PublishArtifact marks the latest version published and saves a copy of
// that version into the library — the same flow as `gg artifact publish`.
func (w *Runtime) PublishArtifact(id string) (PublishResult, error) {
	store, err := w.artifactStore()
	if err != nil {
		return PublishResult{}, err
	}
	if w.libraryStore == nil {
		return PublishResult{}, fmt.Errorf("library store is not available")
	}
	result, err := PublishArtifact(store, w.libraryStore, id)
	return result, w.wrapArtifactError(err, id)
}

func (w *Runtime) wrapArtifactError(err error, id string) error {
	if errors.Is(err, artifact.ErrNotFound) {
		return wrapError(ErrorArtifactNotFound, false, err, "artifact %q not found", id)
	}
	return err
}
