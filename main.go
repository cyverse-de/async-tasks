package main

import (
	"context"
	_ "expvar"
	"flag"
	"fmt"
	"strings"
	"time"

	"net/http"
	"os/signal"
	"syscall"

	"github.com/gorilla/mux"

	"github.com/cyverse-de/async-tasks/database"
	"github.com/cyverse-de/async-tasks/internal/logutil"
	"github.com/cyverse-de/go-mod/otelutils"

	"github.com/cyverse-de/async-tasks/behaviors/statuschangetimeout"

	"github.com/cyverse-de/configurate"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
)

var log = logrus.WithFields(logrus.Fields{
	"service": "async-tasks",
	"art-id":  "async-tasks",
	"group":   "org.cyverse",
})

const serviceName = "async-tasks"
const otelName = "github.com/cyverse-de/async-tasks"

func init() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
}

func makeRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(otelmux.Middleware("async-tasks"))
	router.Handle("/debug/vars", http.DefaultServeMux)
	router.HandleFunc("/", func(writer http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprintf(writer, "Hello from async-tasks.\n"); err != nil {
			log.Error(err)
		}
	}).Methods("GET")

	return router
}

func fixAddr(addr string) string {
	if !strings.HasPrefix(addr, ":") {
		return fmt.Sprintf(":%s", addr)
	}
	return addr
}

func main() {
	var (
		cfgPath = flag.String("config", "/etc/iplant/de/async-tasks.yml", "The path to the config file")
		port    = flag.String("port", "60000", "The port number to listen on")
		err     error
		cfg     *viper.Viper
	)

	flag.Parse()

	if *cfgPath == "" {
		log.Fatal("--config must not be the empty string")
	}

	var tracerCtx, cancel = context.WithCancel(context.Background())
	defer cancel()
	shutdown := otelutils.TracerProviderFromEnv(tracerCtx, serviceName, func(e error) { log.Fatal(e) })
	defer shutdown()

	if cfg, err = configurate.Init(*cfgPath); err != nil {
		log.Fatal(err.Error())
	}

	dburi := cfg.GetString("db.uri")

	db, err := database.SetupDB(dburi, log)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer logutil.LogIfError(log, db.Close)

	count, err := db.GetCount(context.Background())
	if err != nil {
		log.Fatal(err.Error())
	}
	log.Infof("There are %d async tasks in the database", count)

	// Make periodic updater
	updater := NewAsyncTasksUpdater(db)
	updater.AddBehavior("statuschangetimeout", statuschangetimeout.Processor)

	// Cancel on SIGTERM/SIGINT so in-flight work stops and releases its locks
	// before the process exits (e.g. during a rolling deploy).
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stopSignals()

	// running twice a minute catches timeouts measured in minutes reasonably
	// promptly; shorten the interval if finer precision is needed
	// the 10 minute pass timeout is a long timeout we can use to clear out totally stuck jobs
	updaterDone := make(chan struct{})
	go func() {
		defer close(updaterDone)
		runPeriodic(sigCtx, 30*time.Second, 10*time.Minute, func(ctx context.Context, t time.Time) error {
			return updater.DoPeriodicUpdate(ctx, t, db)
		})
	}()

	// Make HTTP listeners
	router := makeRouter()

	app := NewAsyncTasksApp(db, router)
	log.Debug(app)

	server := &http.Server{Addr: fixAddr(*port), Handler: router}
	serverErr := make(chan error, 1)
	go func() {
		log.Infof("Starting to listen on port %s", *port)
		serverErr <- server.ListenAndServe()
	}()

	// Stay under the default 30s Kubernetes termination grace period.
	const shutdownTimeout = 25 * time.Second

	select {
	case err := <-serverErr:
		stopSignals() // also cancels sigCtx, stopping the updater
		if !waitOrTimeout(updaterDone, shutdownTimeout) {
			log.Warn("Timed out waiting for the periodic updater to stop")
		}
		log.Fatal(err)
	case <-sigCtx.Done():
		log.Info("Shutting down")
	}
	stopSignals() // restore default signal handling so a second signal forces an exit

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error(err)
	}
	// Wait for the in-flight pass to stop and release its locks, within the same budget.
	select {
	case <-updaterDone:
	case <-shutdownCtx.Done():
		log.Warn("Timed out waiting for the periodic updater to stop")
	}
}
