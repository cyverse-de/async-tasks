package main

import (
	"context"
	_ "expvar"
	"flag"
	"fmt"
	"strings"
	"time"

	"net/http"

	"github.com/gorilla/mux"

	"github.com/cyverse-de/async-tasks/database"
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
		fmt.Fprintf(writer, "Hello from async-tasks.\n")
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
	defer db.Close()

	count, err := db.GetCount(context.Background())
	if err != nil {
		log.Fatal(err.Error())
	}
	log.Infof("There are %d async tasks in the database", count)

	// Make periodic updater
	updater := NewAsyncTasksUpdater(db)
	updater.AddBehavior("statuschangetimeout", statuschangetimeout.Processor)

	ticker := time.NewTicker(30 * time.Second) // twice a minute means minutely updates behave basically decently, if we need faster we can change this
	defer ticker.Stop()

	go func() {
		for {
			t := <-ticker.C
			log.Infof("Got periodic timer tick: %s", t)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute) // long timeout we can use to clear out totally stuck jobs

			err := updater.DoPeriodicUpdate(ctx, t, db)
			if err != nil {
				log.Error(err)
			}
			cancel()
		}
	}()

	// Make HTTP listeners
	router := makeRouter()

	app := NewAsyncTasksApp(db, router)
	log.Debug(app)

	log.Infof("Starting to listen on port %s", *port)
	log.Fatal(http.ListenAndServe(fixAddr(*port), router))
}
