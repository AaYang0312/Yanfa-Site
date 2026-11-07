// Package release owns immutable artifacts and the only live authority, active.json.
// A Handle is a read-only view: callers must not mutate Catalog's nested values.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,120}$`)

type Handle struct {
	catalog     contract.CatalogSnapshot
	manifest    contract.Manifest
	manifestSHA string
	directory   string
	routes      map[string]bool
	resources   map[string]contract.ManifestResource
}

func (h *Handle) ID() string          { return h.catalog.ReleaseID }
func (h *Handle) SHA256() string      { return h.manifest.SnapshotSHA256 }
func (h *Handle) CodeVersion() string { return h.manifest.CodeVersion }

// Catalog returns a borrowed read-only snapshot, never workspace data. Its slices
// are shared for allocation-free request capture; consumers must not mutate them.
func (h *Handle) Catalog() contract.CatalogSnapshot { return h.catalog }
func (h *Handle) ManifestSHA256() string            { return h.manifestSHA }
func (h *Handle) HasRoute(name string) bool         { return h.routes[name] }
func (h *Handle) Open(name string) (*os.File, error) {
	if !h.routes[name] {
		if _, ok := h.resources[name]; !ok {
			return nil, fs.ErrNotExist
		}
	}
	root, err := os.OpenRoot(filepath.Join(h.directory, "site"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(name)
}
func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func artifactError() error {
	return contract.NewBusinessError(503, "ARTIFACT_UNAVAILABLE", "发布产物校验失败。")
}
func Changed() error {
	return contract.NewBusinessError(409, "CATALOG_CHANGED", "官网资料已变化，请刷新后继续。")
}
func unavailable() error {
	return contract.NewBusinessError(503, "SITE_UNAVAILABLE", "官网尚无可用的已发布版本。")
}
func safe(name string) bool {
	return fs.ValidPath(name) && !strings.Contains(name, "\\") && !strings.HasPrefix(name, ".")
}

// Load validates exact private snapshot bytes, manifest identities, every public
// resource digest, and all HTML routes. Nothing outside the manifest is served.
func Load(dataDir, id string) (*Handle, error) {
	if !identifier.MatchString(id) {
		return nil, artifactError()
	}
	directory := filepath.Join(dataDir, "artifacts", id)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, artifactError()
	}
	defer root.Close()
	snapshotBytes, err := root.ReadFile("catalog.json")
	if err != nil {
		return nil, artifactError()
	}
	snapshot, err := contract.Decode[contract.CatalogSnapshot]("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", snapshotBytes)
	if err != nil {
		return nil, artifactError()
	}
	manifestBytes, err := root.ReadFile("manifest.json")
	if err != nil {
		return nil, artifactError()
	}
	manifest, err := contract.Decode[contract.Manifest]("catalog-snapshot.schema.json#/$defs/Manifest", manifestBytes)
	if err != nil {
		return nil, artifactError()
	}
	if snapshot.ReleaseID != id || manifest.CandidateID != id || manifest.WorkspaceRevision != snapshot.WorkspaceRevision || manifest.SnapshotSHA256 != Hash(snapshotBytes) {
		return nil, artifactError()
	}
	h := &Handle{catalog: snapshot, manifest: manifest, manifestSHA: Hash(manifestBytes), directory: directory, routes: map[string]bool{}, resources: map[string]contract.ManifestResource{}}
	for _, name := range manifest.Routes {
		if !safe(name) || !strings.HasSuffix(name, ".html") {
			return nil, artifactError()
		}
		b, e := root.ReadFile("site/" + name)
		if e != nil || !strings.Contains(string(b), `name="release-id" content="`+id+`"`) {
			return nil, artifactError()
		}
		h.routes[name] = true
	}
	if !h.routes["index.html"] {
		return nil, artifactError()
	}
	for _, resource := range manifest.Resources {
		if !safe(resource.Path) || h.routes[resource.Path] || strings.HasSuffix(resource.Path, ".html") {
			return nil, artifactError()
		}
		f, e := root.Open("site/" + resource.Path)
		if e != nil {
			return nil, artifactError()
		}
		stat, e := f.Stat()
		hash := sha256.New()
		var size int64
		if e == nil && stat.Mode().IsRegular() {
			size, e = io.Copy(hash, f)
		} else {
			e = artifactError()
		}
		f.Close()
		if e != nil || size != resource.Bytes || hex.EncodeToString(hash.Sum(nil)) != resource.SHA256 {
			return nil, artifactError()
		}
		h.resources[resource.Path] = resource
	}
	// An original image is authorized only if a listed product references it.
	publicMedia := map[string]bool{}
	for _, p := range snapshot.Products {
		if p.Visibility == "listed" {
			for _, image := range p.Images {
				ext := image.Format
				if ext == "jpeg" {
					ext = "jpg"
				}
				publicMedia["media/"+image.SHA256+"."+ext] = true
			}
		}
	}
	for name := range h.resources {
		if strings.HasPrefix(name, "media/") && !publicMedia[name] {
			return nil, artifactError()
		}
	}
	for name := range publicMedia {
		if _, ok := h.resources[name]; !ok {
			return nil, artifactError()
		}
	}
	return h, nil
}

type state struct {
	active     contract.ActiveRelease
	current    *Handle
	handles    map[string]*Handle
	resources  map[string]*Handle
	historical map[string]bool
}
type Manager struct {
	dataDir string
	gate    *coordination.Gate
	live    atomic.Pointer[state]
	broken  atomic.Bool
}

func New(dataDir string, gate *coordination.Gate) *Manager {
	return &Manager{dataDir: dataDir, gate: gate}
}
func (m *Manager) Capture(expectedID string) (*Handle, error) {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return nil, unavailable()
	}
	if expectedID != "" && s.current.ID() != expectedID {
		return nil, Changed()
	}
	return s.current, nil
}

// BeginResponse holds the shared read lock only through header/first-block
// submission. The caller sends remaining bytes after this method returns.
func (m *Manager) BeginResponse(expectedID string, start func() error) error {
	m.gate.RLock()
	defer m.gate.RUnlock()
	if _, err := m.Capture(expectedID); err != nil {
		return err
	}
	return start()
}
func (m *Manager) ActiveID() *string {
	h, err := m.Capture("")
	if err != nil {
		return nil
	}
	return new(h.ID())
}

// OperationID identifies an activation, including a rollback to the same release.
// Empty identifies the initial unpublished state; it is not a release ID.
func (m *Manager) OperationID() string {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return ""
	}
	return s.active.OperationID
}
func (m *Manager) Status() string {
	if m.broken.Load() {
		return "unavailable"
	}
	if m.live.Load() == nil {
		return "uninitialized"
	}
	return "ready"
}
func (m *Manager) Active() *contract.ActiveRelease {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return nil
	}
	a := s.active
	a.SuccessfulReleaseIDs = slices.Clone(a.SuccessfulReleaseIDs)
	a.PreviouslyPublishedRoutes = slices.Clone(a.PreviouslyPublishedRoutes)
	return &a
}
func (m *Manager) Successful(id string) bool {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return false
	}
	_, ok := s.handles[id]
	return ok
}

// Retained returns an already verified successful artifact, never a candidate.
func (m *Manager) Retained(id string) (*Handle, error) {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return nil, unavailable()
	}
	h := s.handles[id]
	if h == nil {
		return nil, artifactError()
	}
	return h, nil
}
func (m *Manager) Historical(slug string) bool {
	s := m.live.Load()
	return s != nil && !m.broken.Load() && s.historical[slug]
}
func (m *Manager) Resource(name string) (*Handle, bool) {
	s := m.live.Load()
	if s == nil || m.broken.Load() {
		return nil, false
	}
	h, ok := s.resources[name]
	return h, ok
}
func assemble(a contract.ActiveRelease, handles map[string]*Handle) (*state, error) {
	current := handles[a.ReleaseID]
	if current == nil || current.SHA256() != a.SnapshotSHA256 {
		return nil, artifactError()
	}
	s := &state{active: a, current: current, handles: handles, resources: map[string]*Handle{}, historical: map[string]bool{}}
	routes := map[string]string{}
	for _, id := range a.SuccessfulReleaseIDs {
		h := handles[id]
		if h == nil {
			return nil, artifactError()
		}
		for name := range h.resources {
			s.resources[name] = h
		}
		for _, p := range h.catalog.Products {
			if p.Visibility == "listed" {
				routes[p.Slug] = p.ID
			}
		}
	}
	for _, r := range a.PreviouslyPublishedRoutes {
		if owner, ok := routes[r.Slug]; !ok || owner != r.ProductID {
			return nil, artifactError()
		}
		s.historical[r.Slug] = true
		delete(routes, r.Slug)
	}
	if len(routes) != 0 {
		return nil, artifactError()
	}
	for name := range current.resources {
		s.resources[name] = current
	}
	return s, nil
}
func (m *Manager) Restore() error {
	b, err := os.ReadFile(filepath.Join(m.dataDir, "state", "active.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		m.broken.Store(true)
		return artifactError()
	}
	a, err := contract.Decode[contract.ActiveRelease]("catalog-snapshot.schema.json#/$defs/ActiveRelease", b)
	if err != nil {
		m.broken.Store(true)
		return artifactError()
	}
	handles := map[string]*Handle{}
	for _, id := range a.SuccessfulReleaseIDs {
		h, e := Load(m.dataDir, id)
		if e != nil {
			m.broken.Store(true)
			return e
		}
		handles[id] = h
	}
	s, err := assemble(a, handles)
	if err != nil {
		m.broken.Store(true)
		return err
	}
	m.live.Store(s)
	return nil
}

// Prepare performs filesystem work before taking the short activation lock.
func (m *Manager) Prepare(h *Handle, operation string) (*Prepared, error) {
	if m.broken.Load() {
		return nil, artifactError()
	}
	a := contract.ActiveRelease{SchemaVersion: 1, ReleaseID: h.ID(), SnapshotSHA256: h.SHA256(), OperationID: operation, ActivatedAt: time.Now().UTC().Format(time.RFC3339Nano), SuccessfulReleaseIDs: []string{}, PreviouslyPublishedRoutes: []contract.PublishedRoute{}}
	handles := map[string]*Handle{}
	baseline := ""
	if old := m.live.Load(); old != nil {
		baseline = old.active.OperationID
		a.SuccessfulReleaseIDs = slices.Clone(old.active.SuccessfulReleaseIDs)
		a.PreviouslyPublishedRoutes = slices.Clone(old.active.PreviouslyPublishedRoutes)
		for id, previous := range old.handles {
			handles[id] = previous
		}
	}
	if !slices.Contains(a.SuccessfulReleaseIDs, h.ID()) {
		a.SuccessfulReleaseIDs = append(a.SuccessfulReleaseIDs, h.ID())
	}
	handles[h.ID()] = h
	known := map[string]bool{}
	for _, r := range a.PreviouslyPublishedRoutes {
		known[r.Slug] = true
	}
	for _, p := range h.catalog.Products {
		if p.Visibility == "listed" && !known[p.Slug] {
			a.PreviouslyPublishedRoutes = append(a.PreviouslyPublishedRoutes, contract.PublishedRoute{ProductID: p.ID, Slug: p.Slug})
			known[p.Slug] = true
		}
	}
	slices.SortFunc(a.PreviouslyPublishedRoutes, func(x, y contract.PublishedRoute) int { return strings.Compare(x.Slug, y.Slug) })
	s, err := assemble(a, handles)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return &Prepared{s: s, bytes: b, baseline: baseline}, nil
}

type Prepared struct {
	s        *state
	bytes    []byte
	baseline string
}

// ActivateLocked requires the caller's shared Gate write lock. After rename the
// pointer is authoritative even if a later durability/DB operation fails.
func (m *Manager) ActivateLocked(ctx context.Context, p *Prepared) (bool, error) {
	id := ""
	if old := m.live.Load(); old != nil {
		id = old.active.OperationID
	}
	if id != p.baseline {
		return false, Changed()
	}
	directory := filepath.Join(m.dataDir, "state")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return false, err
	}
	if err := Barrier(ctx, "before-active-temp"); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(directory, ".active-")
	if err != nil {
		return false, err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(p.bytes)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	if err = Barrier(ctx, "after-temp-fsync-before-rename"); err != nil {
		return false, err
	}
	if err = os.Rename(name, filepath.Join(directory, "active.json")); err != nil {
		return false, err
	}
	syncErr := SyncDir(directory)
	if err = Barrier(ctx, "after-rename-before-memory/DB"); err != nil {
		m.broken.Store(true)
		return true, err
	}
	m.live.Store(p.s)
	if err = Barrier(ctx, "after-memory-before-DB"); err != nil {
		return true, err
	}
	return true, syncErr
}
func SyncDir(directory string) error {
	f, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Seal syncs all files/directories before the artifact's single rename.
func Seal(staging, destination string) error {
	err := filepath.WalkDir(staging, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in artifact")
		}
		if d.IsDir() {
			return nil
		}
		f, e := os.Open(name)
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		return e
	})
	if err != nil {
		return err
	}
	err = filepath.WalkDir(staging, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return SyncDir(name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err = os.Lstat(destination); !errors.Is(err, fs.ErrNotExist) {
		return artifactError()
	}
	if err = os.Rename(staging, destination); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(destination))
}

// Route maps only conventional static HTML URLs; path traversal is never cleaned
// into a different authorized route.
func Route(urlPath string) string {
	if strings.Contains(urlPath, "\\") || !strings.HasPrefix(urlPath, "/") {
		return ""
	}
	name := strings.TrimPrefix(urlPath, "/")
	if name == "" {
		return "index.html"
	}
	if strings.Contains(name, "//") {
		return ""
	}
	if strings.HasSuffix(name, "/") {
		name += "index.html"
	} else if path.Ext(name) == "" {
		name += "/index.html"
	}
	if !safe(name) {
		return ""
	}
	return name
}
