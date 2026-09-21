package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Repository is the application-facing session persistence boundary. The
// current implementation is file-backed; callers do not need to know how a new
// session path is allocated or how an ID is resolved.
type Repository interface {
	Create(cwd string) (*Store, Loaded, error)
	OpenPath(path, cwd string) (*Store, Loaded, error)
	OpenForCWD(cwd, target string, allowPath bool) (*Store, Loaded, error)
	OpenLatest(cwd string) (*Store, Loaded, error)
	List(cwd string) ([]Info, error)
}

type FileRepository struct {
	root string
}

func NewFileRepository(root string) *FileRepository {
	return &FileRepository{root: root}
}

func (r *FileRepository) Create(cwd string) (*Store, Loaded, error) {
	filename := fmt.Sprintf("%d.jsonl", time.Now().UnixNano())
	return r.OpenPath(filepath.Join(CWDDir(r.root, cwd), filename), cwd)
}

func (r *FileRepository) OpenPath(path, cwd string) (*Store, Loaded, error) {
	if strings.TrimSpace(path) == "" {
		return nil, Loaded{}, fmt.Errorf("session path is required")
	}
	store, err := NewStore(path, cwd)
	if err != nil {
		return nil, Loaded{}, err
	}
	loaded, err := Load(store.Path())
	if err != nil {
		return nil, Loaded{}, err
	}
	return store, loaded, nil
}

func (r *FileRepository) OpenForCWD(cwd, target string, allowPath bool) (*Store, Loaded, error) {
	if !allowPath && looksLikePath(strings.TrimSpace(target)) {
		return nil, Loaded{}, fmt.Errorf("session must be addressed by id")
	}
	path, err := FindForCWD(r.root, cwd, target)
	if err != nil {
		return nil, Loaded{}, err
	}
	return r.OpenPath(path, cwd)
}

func (r *FileRepository) OpenLatest(cwd string) (*Store, Loaded, error) {
	latest, err := LatestForCWD(r.root, cwd)
	if err != nil {
		return nil, Loaded{}, err
	}
	return r.OpenPath(latest.Path, cwd)
}

func (r *FileRepository) List(cwd string) ([]Info, error) {
	return ListForCWD(r.root, cwd)
}
