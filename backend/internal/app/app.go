// Package app is the single composition and lifecycle owner. All business
// services share Store, Auth, Gate and the verified active release authority.
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/guide"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/media"
	"yuanfa-official/backend/internal/publication"
	"yuanfa-official/backend/internal/release"
	"yuanfa-official/backend/internal/storage"
)

var ErrRestoreIncomplete = errors.New("RESTORE_INCOMPLETE")

type App struct {
	Config      config.Config
	Store       *storage.Store
	Auth        *auth.Service
	Gate        *coordination.Gate
	Content     *content.Service
	Media       *media.Service
	Publication *publication.Service
	Releases    *release.Manager
	Catalog     *catalog.Service
	Guide       *guide.Service
	Mux         *http.ServeMux
	siteConfig  contract.SiteConfig
	lock        *coordination.DataDirLock
	ctx         context.Context
	cancel      context.CancelFunc
	server      *http.Server
	requests    sync.WaitGroup
	stopHooks   []func() error
	closeOnce   sync.Once
	closeErr    error
}

// Open locks before SQLite access and refuses incomplete restores before opening
// the database. The returned application's Close owns the complete lock window.
func Open(ctx context.Context, c config.Config) (*App, error) {
	lock, err := coordination.AcquireDataDir(c.DataDir)
	if err != nil {
		return nil, err
	}
	dir, err := lock.Dir()
	if err != nil {
		lock.Close()
		return nil, err
	}
	c.DataDir = dir
	if _, err = os.Lstat(filepath.Join(dir, ".restore-incomplete")); err == nil {
		lock.Close()
		return nil, ErrRestoreIncomplete
	} else if !errors.Is(err, os.ErrNotExist) {
		lock.Close()
		return nil, err
	}
	store, err := storage.Open(ctx, lock)
	if err != nil {
		lock.Close()
		return nil, err
	}
	identity, err := auth.New(store.DB())
	if err != nil {
		store.Close()
		lock.Close()
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	a := &App{Config: c, Store: store, Auth: identity, Gate: &coordination.Gate{}, Mux: http.NewServeMux(), lock: lock, ctx: lifetime, cancel: cancel}
	a.Media = media.New(store.DB(), identity, a.Gate, c.DataDir)
	a.Content = content.New(store.DB(), identity, a.Gate, a.Media, c.DataDir, c.PurchaseAllowedHosts)
	a.Releases = release.New(c.DataDir, a.Gate)
	a.Catalog = catalog.New(a.Releases, c.PurchaseAllowedHosts)
	code, err := publication.CodeVersion()
	if err != nil {
		cancel()
		store.Close()
		lock.Close()
		return nil, err
	}
	a.Publication = publication.New(store.DB(), identity, a.Content, a.Gate, a.Releases, c, code)
	a.OnStop(a.Publication.Close)
	if c.SiteConfigPath != "" {
		b, readErr := os.ReadFile(c.SiteConfigPath)
		if readErr == nil {
			a.siteConfig, readErr = contract.Decode[contract.SiteConfig]("guide.schema.json#/$defs/SiteConfig", b)
		}
		if readErr != nil {
			_ = a.Close()
			return nil, readErr
		}
	}
	a.Guide = guide.New(a.Catalog, a.siteConfig.ContactHref)
	if err = a.routes(); err != nil {
		_ = a.Close()
		return nil, err
	}
	return a, nil
}
func (a *App) Context() context.Context { return a.ctx }

// OnStop registers a background worker's cancel-and-join callback before Serve.
// Callbacks run in reverse order after HTTP stops, before DB/lock release. A
// callback must wait for all owned subprocesses even when returning an error.
func (a *App) OnStop(stop func() error) { a.stopHooks = append(a.stopHooks, stop) }

func (a *App) routes() error {
	middleware := httpapi.AuthMiddleware{Auth: a.Auth, Origin: a.Config.SiteURL, SecureCookies: a.Config.SecureCookies}
	httpapi.RegisterContent(a.Mux, middleware, a.Content)
	httpapi.RegisterMedia(a.Mux, middleware, a.Media)
	httpapi.RegisterPublication(a.Mux, middleware, a.Publication)
	httpapi.RegisterPreview(a.Mux, middleware, a.Publication)
	httpapi.RegisterCatalog(a.Mux, a.Releases, a.Catalog)
	httpapi.RegisterPurchase(a.Mux, a.Releases, a.Catalog, a.siteConfig.ContactHref)
	model, err := guide.NewModel(a.ctx, a.Config.ModelBaseURL, a.Config.ModelModel, a.Config.ModelAPIKey)
	if err != nil {
		return errors.New("cannot initialize guide model")
	}
	httpapi.RegisterGuide(a.Mux, a.Releases, a.Guide, httpapi.GuideOptions{Agent: guide.NewAgent(a.Guide, model), Limits: guide.NewLimits(a.Config.GuideIPRPM, a.Config.GuideMaxConcurrency), ClientIP: a.Config.ClientIP})
	if err := httpapi.RegisterMCP(a.Mux, middleware, a.Content, a.Media, a.Publication); err != nil {
		return err
	}
	httpapi.RegisterAuth(a.Mux, middleware, func(r *http.Request) (int64, *string, error) {
		var revision int64
		err := a.Store.DB().QueryRowContext(r.Context(), `SELECT revision FROM workspace WHERE id=1`).Scan(&revision)
		return revision, a.Releases.ActiveID(), err
	})
	a.Mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteJSON(w, 200, contract.HealthLive{Status: "ok"})
	})
	a.Mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		health := contract.HealthReady{Database: "ready", Artifacts: a.Releases.Status(), Model: "unconfigured", ReleaseID: a.Releases.ActiveID()}
		if err := a.Store.DB().PingContext(r.Context()); err != nil {
			health.Database = "unavailable"
		}
		if a.Config.ModelConfigured() {
			health.Model = "configured"
		}
		health.Ready = health.Database == "ready" && health.Artifacts == "ready"
		status := 503
		if health.Ready {
			status = 200
		}
		httpapi.WriteJSON(w, status, health)
	})
	httpapi.RegisterStatic(a.Mux, middleware, a.Publication, a.Releases, a.Config.AdminStaticDir, a.siteConfig.ContactHref, a.siteConfig.Phone)
	return nil
}

// Serve runs the real listener and blocks through graceful shutdown. Shutdown
// cancels request/worker contexts, drains HTTP, joins workers, closes SQLite,
// and finally releases the permanent data-directory lock.
func (a *App) Serve() error {
	// A damaged pointer keeps the management surface available, but no public
	// handle is loaded and ready remains unavailable. Never fall back to sources.
	_ = a.Releases.Restore()
	if err := a.Publication.Start(a.ctx); err != nil {
		_ = a.Close()
		return err
	}
	listener, err := net.Listen("tcp", a.Config.HTTPAddr)
	if err != nil {
		_ = a.Close()
		return err
	}
	a.server = &http.Server{Addr: a.Config.HTTPAddr, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 * 1024, BaseContext: func(net.Listener) context.Context { return a.ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.requests.Add(1)
		defer a.requests.Done()
		a.Mux.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- a.server.Serve(listener) }()
	select {
	case <-a.ctx.Done():
		err = a.Close()
		serveErr := <-done
		if err == nil && !errors.Is(serveErr, http.ErrServerClosed) {
			err = serveErr
		}
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		err = errors.Join(err, a.Close())
	}
	return err
}
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.cancel()
		if a.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := a.server.Shutdown(ctx)
			cancel()
			if err != nil {
				a.closeErr = errors.Join(a.closeErr, err, a.server.Close())
			}
			a.requests.Wait()
		}
		for i := len(a.stopHooks) - 1; i >= 0; i-- {
			a.closeErr = errors.Join(a.closeErr, a.stopHooks[i]())
		}
		a.closeErr = errors.Join(a.closeErr, a.Store.Close())
		a.closeErr = errors.Join(a.closeErr, a.lock.Close())
	})
	return a.closeErr
}
