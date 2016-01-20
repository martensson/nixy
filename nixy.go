package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/peterbourgon/g2s"
	"github.com/thoas/stats"
)

type App struct {
	Tasks []string
}
type Apps struct {
	sync.RWMutex
	Apps map[string]App
}

var apps Apps

type Config struct {
	Port           string
	Xproxy         string
	Marathon       string
	Nginx_config   string
	Nginx_template string
	Nginx_cmd      string
	Statsd         string
}

var config Config
var statsd g2s.Statter

func nixy_reload(w http.ResponseWriter, r *http.Request) {
	log.Println("callback received from Marathon")
	select {
	case callbackqueue <- true: // Add reload to our queue channel, unless it is full of course.
		w.WriteHeader(202)
		fmt.Fprintln(w, "queued")
		return
	default:
		w.WriteHeader(202)
		fmt.Fprintln(w, "queue is full")
		return
	}
}

func nixy_apps(w http.ResponseWriter, r *http.Request) {
	b, _ := json.MarshalIndent(apps, "", "  ")
	w.Write(b)
	return
}

func main() {
	configtoml := flag.String("f", "nixy.toml", "Path to config. (default nixy.toml)")
	flag.Parse()
	file, err := ioutil.ReadFile(*configtoml)
	if err != nil {
		log.Fatal(err)
	}
	err = toml.Unmarshal(file, &config)
	if err != nil {
		log.Fatal("Problem parsing config: ", err)
	}
	if config.Statsd != "" {
		statsd, _ = g2s.Dial("udp", config.Statsd)
	}
	nixystats := stats.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/reload", nixy_reload)
	mux.HandleFunc("/v1/apps", nixy_apps)
	mux.HandleFunc("/v1/stats", func(w http.ResponseWriter, req *http.Request) {
		stats := nixystats.Data()
		b, _ := json.MarshalIndent(stats, "", "  ")
		w.Write(b)
		return
	})
	handler := nixystats.Handler(mux)
	s := &http.Server{
		Addr:    ":" + config.Port,
		Handler: handler,
	}
	callbackworker()
	callbackqueue <- true
	log.Println("Starting nixy on :" + config.Port)
	err = s.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
