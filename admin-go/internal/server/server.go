package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/neuvector/manager/admin-go/internal/config"
	"github.com/neuvector/manager/admin-go/internal/controller"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	controllerClient := controller.New(cfg.Controller.BaseURL, cfg.Controller.TLSVerify, cfg.Controller.Timeout)
	handler, err := newManagedHandler(cfg, logger, controllerClient)
	if err != nil {
		return fmt.Errorf("initialize manager handler: %w", err)
	}
	publicServer := &http.Server{
		Addr: cfg.Server.Address, Handler: handler,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
	}
	certificateSource := "disabled"
	if cfg.Server.TLS {
		tlsConfig, source, err := managerTLSConfig(cfg.Server)
		if err != nil {
			_ = handler.Close()
			return err
		}
		publicServer.TLSConfig = tlsConfig
		certificateSource = source
		if source == tlsCertificateEphemeral {
			logger.Warn("configured TLS certificate pair is unavailable; using an ephemeral self-signed certificate")
		}
	}

	var ready atomic.Bool
	servers := []*http.Server{publicServer}
	if cfg.Server.InternalAddress != "" {
		servers = append(servers, &http.Server{
			Addr: cfg.Server.InternalAddress, Handler: NewHealthHandler(ready.Load),
			MaxHeaderBytes: cfg.Server.MaxHeaderBytes, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		})
	}

	errorsChannel := make(chan error, len(servers))
	for index, current := range servers {
		isPublic := index == 0
		go func(instance *http.Server) {
			var err error
			if isPublic && cfg.Server.TLS {
				err = instance.ListenAndServeTLS("", "")
			} else {
				err = instance.ListenAndServe()
			}
			errorsChannel <- err
		}(current)
	}
	ready.Store(true)
	logger.Info("manager started", "address", cfg.Server.Address, "tls", cfg.Server.TLS, "tls_certificate_source", certificateSource, "path_prefix", cfg.Server.PathPrefix)

	var serveError error
	select {
	case <-ctx.Done():
		ready.Store(false)
	case err := <-errorsChannel:
		ready.Store(false)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveError = fmt.Errorf("serve manager: %w", err)
		}
	}
	if err := handler.Close(); err != nil {
		serveError = errors.Join(serveError, fmt.Errorf("close manager handler: %w", err))
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()
	var shutdownErrors []error
	for _, current := range servers {
		if err := current.Shutdown(shutdownContext); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	return errors.Join(append([]error{serveError}, shutdownErrors...)...)
}
