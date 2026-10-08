package server

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lavale1012/ss-go-wrkr/routes"
)

const (
	defaultPort     = "8080"
	shutdownTimeout = 10 * time.Second
)

// New builds the router with all routes registered.
func New() *gin.Engine {
	r := gin.Default()
	routes.Register(r)
	return r
}

// Run starts the server on PORT, falling back to 8080, and blocks until it
// fails or receives SIGINT/SIGTERM. On a signal it stops accepting new
// connections and gives in-flight requests up to shutdownTimeout to finish.
func Run() error {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	srv := &http.Server{Addr: ":" + port, Handler: New()}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	// Restore default signal handling so a second signal exits immediately.
	stop()

	log.Println("shutting down, waiting for in-flight requests")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Println("server stopped")
	return nil
}
