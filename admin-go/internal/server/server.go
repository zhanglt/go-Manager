package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/neuvector/manager/admin-go/internal/config"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"golang.org/x/net/netutil"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if cfg.Server.MaxConnections < 1 {
		return errors.New("server max connections must be positive")
	}
	controllerClient := controller.New(cfg.Controller.BaseURL, cfg.Controller.TLSVerify, cfg.Controller.Timeout)
	handler, err := newManagedHandler(cfg, logger, controllerClient)
	if err != nil {
		return fmt.Errorf("initialize manager handler: %w", err)
	}
	publicServer := &http.Server{
		Addr: cfg.Server.Address, Handler: handler,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout: cfg.Server.IdleTimeout,
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
			Addr: cfg.Server.InternalAddress, Handler: NewHealthHandler(ready.Load, handler.metrics),
			MaxHeaderBytes: cfg.Server.MaxHeaderBytes, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
			ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout,
			IdleTimeout: cfg.Server.IdleTimeout,
		})
	}

	listeners := make([]net.Listener, 0, len(servers))
	for index, current := range servers {
		listener, err := net.Listen("tcp", current.Addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			_ = handler.Close()
			return fmt.Errorf("listen on %s: %w", current.Addr, err)
		}
		listener = netutil.LimitListener(listener, cfg.Server.MaxConnections)
		if index == 0 && cfg.Server.TLS {
			listener = tls.NewListener(listener, current.TLSConfig)
		}
		listeners = append(listeners, listener)
	}

	errorsChannel := make(chan error, len(servers))
	for index, current := range servers {
		go func(instance *http.Server, listener net.Listener) {
			errorsChannel <- instance.Serve(listener)
		}(current, listeners[index])
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
