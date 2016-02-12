package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
		Env          map[string]string `json:"env"`
		HealthChecks []interface{}     `json:"healthChecks"`
	} `json:"apps"`
}

func eventStream() {
	go func() {
		client := &http.Client{
			Timeout:   0 * time.Second,
			Transport: tr,
		}
		ticker := time.NewTicker(1 * time.Second)
		for _ = range ticker.C {
			req, err := http.NewRequest("GET", endpoint+"/v2/events", nil)
			if err != nil {
				log.Printf("Unable to create event stream request: %s\n", err)
				continue
			}
			req.Header.Set("Accept", "text/event-stream")
			if config.User != "" {
				req.SetBasicAuth(config.User, config.Pass)
			}
			resp, err := client.Do(req)
			if err != nil {
				log.Printf("Unable to access Marathon event stream: %s\n", err)
				continue
			}
			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if err != io.EOF {
						log.Printf("Error reading Marathon event: %s\n", err)
					}
					resp.Body.Close()
					break
				}
				if !strings.HasPrefix(line, "event: ") {
					continue
				}
				log.Printf("Marathon event %s received. Triggering new update.", strings.TrimSpace(line[6:]))
				select {
				case eventqueue <- true: // Add reload to our queue channel, unless it is full of course.
				default:
					log.Println("queue is full")
				}

			}
			resp.Body.Close()
			log.Println("Event stream connection was closed. Re-opening...")
		}
	}()
}

func endpointHealth() {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for {
			select {
			case <-ticker.C:
				for _, ep := range config.Marathon {
					client := &http.Client{
						Timeout:   5 * time.Second,
						Transport: tr,
					}
					req, err := http.NewRequest("GET", ep+"/ping", nil)
					if err != nil {
						log.Printf("An error occurred creating endpoint health request: %s\n", err.Error())
						continue
					}
					if config.User != "" {
						req.SetBasicAuth(config.User, config.Pass)
					}
					resp, err := client.Do(req)
					if err != nil {
						log.Printf("Endpoint %s is down: %s\n", ep, err.Error())
						continue
					}
					resp.Body.Close()
					if resp.StatusCode != 200 {
						log.Printf("Endpoint %s is down: status code %d\n", ep, resp.StatusCode)
						continue
					}
					if endpoint != ep {
						endpoint = ep
						log.Printf("Endpoint %s is now active.\n", ep)
					}
					break // no need to continue now.
				}
			}
		}
	}()
}

func eventWorker() {
	go func() {
		// a ticker channel to limit reloads to marathon, 1s is enough for now.
		ticker := time.NewTicker(1 * time.Second)
		for {
			select {
			case <-ticker.C:
				<-eventqueue
				start := time.Now()
				err := reload()
				elapsed := time.Since(start)
				if err != nil {
					log.Println("config update failed")
					if config.Statsd != "" {
						go func() {
							hostname, _ := os.Hostname()
							statsd.Counter(1.0, "nixy."+hostname+".reload.failed", 1)
						}()
					}
				} else {
					log.Printf("config update took %s\n", elapsed)
					if config.Statsd != "" {
						go func(elapsed time.Duration) {
							hostname, _ := os.Hostname()
							statsd.Counter(1.0, "nixy."+hostname+".reload.success", 1)
							statsd.Timing(1.0, "nixy."+hostname+".reload.time", elapsed)
						}(elapsed)
					}
				}
			}
		}
	}()
}

func fetchApps(jsontasks *MarathonTasks, jsonapps *MarathonApps) error {
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
	}
	// take advantage of goroutines and run both reqs concurrent.
	appschn := make(chan error)
	taskschn := make(chan error)
	go func() {
		req, err := http.NewRequest("GET", endpoint+"/v2/tasks", nil)
		if err != nil {
			taskschn <- err
			return
		}
		req.Header.Set("Accept", "application/json")
		if config.User != "" {
			req.SetBasicAuth(config.User, config.Pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			taskschn <- err
			return
		}
		defer resp.Body.Close()
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&jsontasks)
		if err != nil {
			taskschn <- err
			return
		}
		taskschn <- nil
	}()
	go func() {
		req, err := http.NewRequest("GET", endpoint+"/v2/apps", nil)
		if err != nil {
			appschn <- err
			return
		}
		req.Header.Set("Accept", "application/json")
		if config.User != "" {
			req.SetBasicAuth(config.User, config.Pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			appschn <- err
			return
		}
		defer resp.Body.Close()
		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&jsonapps)
		if err != nil {
			appschn <- err
			return
		}
		appschn <- nil
	}()
	appserr := <-appschn
	taskserr := <-taskschn
	if appserr != nil {
		return appserr
	}
	if taskserr != nil {
		return taskserr
	}
	return nil
}

func syncApps(jsontasks *MarathonTasks, jsonapps *MarathonApps) {
	config.Lock()
	defer config.Unlock()
	config.Apps = make(map[string]App)
	for _, app := range jsonapps.Apps {
		for _, task := range jsontasks.Tasks {
			if task.AppId != app.Id {
				continue
			}
			// Lets skip tasks that does not expose any ports.
			if len(task.Ports) == 0 {
				continue
			}
			if len(app.HealthChecks) > 0 {
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
			if s, ok := config.Apps[app.Id]; ok {
				s.Tasks = append(s.Tasks, task.Host+":"+strconv.FormatInt(task.Ports[0], 10))
				config.Apps[app.Id] = s
			} else {
				var newapp = App{}
				newapp.Tasks = []string{task.Host + ":" + strconv.FormatInt(task.Ports[0], 10)}
				// Create a valid hostname of app id.
				if s, ok := app.Labels["subdomain"]; ok {
					newapp.Host = s
				} else if s, ok := app.Labels["moxy_subdomain"]; ok {
					// to be compatible with moxy
					newapp.Host = s
				} else {
					re := regexp.MustCompile("[^0-9a-z-]")
					newapp.Host = re.ReplaceAllLiteralString(app.Id, "")
				}
				for k, v := range config.Apps {
					if newapp.Host == v.Host {
						log.Printf("%s and %s share same subdomain '%s', ignoring %s.", k, app.Id, v.Host, app.Id)
						continue
					}
				}
				newapp.Labels = app.Labels
				newapp.Env = app.Env
				config.Apps[app.Id] = newapp
			}
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
