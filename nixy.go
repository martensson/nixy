package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
	"github.com/peterbourgon/g2s"
)

type App struct {
	Tasks  []string
	Labels map[string]string
	Env    map[string]string
	Host   string
}

type Config struct {
	sync.RWMutex
	Xproxy         string
	Port           string   `json:"-"`
	Marathon       []string `json:"-"`
	User           string   `json:"-"`
	Pass           string   `json:"-"`
	Nginx_config   string   `json:"-"`
	Nginx_template string   `json:"-"`
	Nginx_cmd      string   `json:"-"`
	Statsd         string   `json:"-"`
	Apps           map[string]App
}

// Global variables
var VERSION string //added by goxc
var config Config
var statsd g2s.Statter
var endpoint string
var sick int
var logger = logrus.New()

// buffer of two, because we dont really need more.
var eventqueue = make(chan bool, 2)

// Global http transport for connection reuse
var tr = &http.Transport{}

func nixy_reload(w http.ResponseWriter, r *http.Request) {
	logger.WithFields(logrus.Fields{
		"client": r.RemoteAddr,
	}).Info("marathon reload triggered")
	select {
	case eventqueue <- true: // Add reload to our queue channel, unless it is full of course.
		w.WriteHeader(202)
		fmt.Fprintln(w, "queued")
		return
	default:
		w.WriteHeader(202)
		fmt.Fprintln(w, "queue is full")
		return
	}
}

func nixy_health(w http.ResponseWriter, r *http.Request) {
	health := make(map[string]string)
	err := checkTmpl()
	if err != nil {
		w.WriteHeader(500)
		health["Template"] = err.Error()
	} else {
		health["Template"] = "OK"
	}
	err = checkConf()
	if err != nil {
		w.WriteHeader(500)
		health["Config"] = err.Error()
	} else {
		health["Config"] = "OK"
	}
	if sick == len(config.Marathon) {
		w.WriteHeader(500)
		health["Endpoints"] = "All endpoints are down"
	} else {
		health["Endpoints"] = strconv.Itoa(sick) + " SICK"
	}
	w.Header().Add("Content-Type", "application/json; charset=utf-8")
	b, _ := json.MarshalIndent(health, "", "  ")
	w.Write(b)
	return
}

func nixy_config(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json; charset=utf-8")
	b, _ := json.MarshalIndent(config, "", "  ")
	w.Write(b)
	return
}

func nixy_version(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "nixy "+VERSION)
	return
}

func main() {
	configtoml := flag.String("f", "nixy.toml", "Path to config. (default nixy.toml)")
	version := flag.Bool("v", false, "prints current nixy version")
	flag.Parse()
	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}
	file, err := ioutil.ReadFile(*configtoml)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatal("problem opening toml config")
	}
	err = toml.Unmarshal(file, &config)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Fatal("problem parsing config")
	}
	if config.Statsd != "" {
		statsd, _ = g2s.Dial("udp", config.Statsd)
	}
	mux := mux.NewRouter()
	mux.HandleFunc("/", nixy_version)
	mux.HandleFunc("/v1/reload", nixy_reload)
	mux.HandleFunc("/v1/config", nixy_config)
	mux.HandleFunc("/v1/health", nixy_health)
	s := &http.Server{
		Addr:    ":" + config.Port,
		Handler: mux,
	}
	endpoint = config.Marathon[0] // lets start with the first node.
	endpointHealth()
	eventStream()
	eventWorker()
	logger.Info("starting nixy on :" + config.Port)
	err = s.ListenAndServe()
	if err != nil {
		log.Fatal(err)
	}
}
