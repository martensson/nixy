package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"text/template"
	"time"
)

type MarathonTasks struct {
	Tasks []struct {
		AppId              string `json:"appId"`
		HealthCheckResults []struct {
			Alive bool `json:"alive"`
		} `json:"healthCheckResults"`
		Host         string  `json:"host"`
		Id           string  `json:"id"`
		Ports        []int64 `json:"ports"`
		ServicePorts []int64 `json:"servicePorts"`
		StagedAt     string  `json:"stagedAt"`
		StartedAt    string  `json:"startedAt"`
		Version      string  `json:"version"`
	} `json:"tasks"`
}

type MarathonApps struct {
	Apps []struct {
		Id           string            `json:"id"`
		Labels       map[string]string `json:"labels"`
		HealthChecks []interface{}     `json:"healthChecks"`
	} `json:"apps"`
}

// buffer of two, because we dont really need more.
var callbackqueue = make(chan bool, 2)

func callbackworker() {
	// a ticker channel to limit reloads to marathon, 1s is enough for now.
	ticker := time.NewTicker(1000 * time.Millisecond)
	go func() {
		for {
			select {
			case <-ticker.C:
				<-callbackqueue
				err := reload()
				if err != nil {
					log.Println("config failed")
					if config.Statsd != "" {
						go func() {
							hostname, _ := os.Hostname()
							statsd.Counter(1.0, "nixy."+hostname+".reload.failed", 1)
						}()
					}
				} else {
					log.Println("config updated")
					if config.Statsd != "" {
						go func() {
							hostname, _ := os.Hostname()
							statsd.Counter(1.0, "nixy."+hostname+".reload.success", 1)
						}()
					}
				}
			}
		}
	}()
}

func fetchApps(jsontasks *MarathonTasks, jsonapps *MarathonApps) error {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	r, _ := http.NewRequest("GET", config.Marathon+"/v2/tasks", nil)
	r.Header.Set("Accept", "application/json")
	resp, err := client.Do(r)
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&jsontasks)
	if err != nil {
		return err
	}
	r, _ = http.NewRequest("GET", config.Marathon+"/v2/apps", nil)
	r.Header.Set("Accept", "application/json")
	resp, err = client.Do(r)
	defer resp.Body.Close()
	if err != nil {
		return err
	}
	decoder = json.NewDecoder(resp.Body)
	err = decoder.Decode(&jsonapps)
	if err != nil {
		return err
	}
	return nil
}

func syncApps(jsontasks *MarathonTasks, jsonapps *MarathonApps) {
	config.Lock()
	defer config.Unlock()
	config.Apps = make(map[string]App)
	for _, task := range jsontasks.Tasks {
		// Use regex to remove characters that are not allowed in hostnames
		re := regexp.MustCompile("[^0-9a-z-]")
		appid := re.ReplaceAllLiteralString(task.AppId, "")
		apphealth := false
		for _, v := range jsonapps.Apps {
			if v.Id == task.AppId {
				if s, ok := v.Labels["subdomain"]; ok {
					appid = s
				}
				// this is temporary to support moxy subdomains.
				if s, ok := v.Labels["moxy_subdomain"]; ok {
					appid = s
				}
				if len(v.HealthChecks) > 0 {
					apphealth = true
				}
			}
		}
		// Lets skip tasks that does not expose any ports.
		if len(task.Ports) == 0 {
			continue
		}
		if apphealth {
			if len(task.HealthCheckResults) == 0 {
				// this means tasks is being deployed but not yet monitored as alive. Assume down.
				continue
			}
			alive := true
			for _, health := range task.HealthCheckResults {
				// check if health check is alive
				if health.Alive == false {
					alive = false
				}
			}
			if alive != true {
				// at least one health check has failed. Assume down.
				continue
			}
		}
		if s, ok := config.Apps[appid]; ok {
			s.Tasks = append(s.Tasks, task.Host+":"+strconv.FormatInt(task.Ports[0], 10))
			config.Apps[appid] = s
		} else {
			var s = App{}
			s.Tasks = []string{task.Host + ":" + strconv.FormatInt(task.Ports[0], 10)}
			config.Apps[appid] = s
		}
	}
}

func writeConf() error {
	t, err := template.New(filepath.Base(config.Nginx_template)).ParseFiles(config.Nginx_template)
	if err != nil {
		return err
	}
	f, err := os.Create(config.Nginx_config)
	defer f.Close()
	if err != nil {
		return err
	}
	err = t.Execute(f, config)
	if err != nil {
		return err
	}
	return nil
}

func checkTmpl() error {
	t, err := template.New(filepath.Base(config.Nginx_template)).ParseFiles(config.Nginx_template)
	if err != nil {
		return err
	}
	err = t.Execute(ioutil.Discard, config)
	if err != nil {
		return err
	}
	return nil
}

func checkConf() error {
	cmd := exec.Command(config.Nginx_cmd, "-c", config.Nginx_config, "-t")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run() // will wait for command to return
	if err != nil {
		msg := fmt.Sprint(err) + ": " + stderr.String()
		errstd := errors.New(msg)
		return errstd
	}
	return nil
}

func reloadNginx() error {
	cmd := exec.Command(config.Nginx_cmd, "-s", "reload")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run() // will wait for command to return
	if err != nil {
		msg := fmt.Sprint(err) + ": " + stderr.String()
		errstd := errors.New(msg)
		return errstd
	}
	return nil
}

func reload() error {
	jsontasks := MarathonTasks{}
	jsonapps := MarathonApps{}
	err := fetchApps(&jsontasks, &jsonapps)
	if err != nil {
		log.Println("Unable to sync from Marathon:", err)
		return err
	}
	syncApps(&jsontasks, &jsonapps)
	err = writeConf()
	if err != nil {
		log.Println("Unable to generate nginx config:", err)
		return err
	}
	err = reloadNginx()
	if err != nil {
		log.Println("Unable to reload nginx:", err)
		return err
	}
	return nil
}
