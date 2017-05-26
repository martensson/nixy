package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"
	"time"

	"github.com/Sirupsen/logrus"
)

// Struct for our apps nested with tasks.
type MarathonApps struct {
	Apps []struct {
		Id              string            `json:"id"`
		Labels          map[string]string `json:"labels"`
		Env             map[string]string `json:"env"`
		HealthChecks    []interface{}     `json:"healthChecks"`
		PortDefinitions []struct {
			Port     int64             `json:"port"`
			Protocol string            `json:"protocol"`
			Labels   map[string]string `json:"labels"`
		} `json:"portDefinitions"`
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
			var endpoint string
			for _, es := range health.Endpoints {
				if es.Healthy == true {
					endpoint = es.Endpoint
					break
				}
			}
			if endpoint == "" {
				logger.Error("all endpoints are down")
				continue
			}
			req, err := http.NewRequest("GET", endpoint+"/v2/events", nil)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"error":    err.Error(),
					"endpoint": endpoint,
				}).Error("unable to create event stream request")
				continue
			}
			req.Header.Set("Accept", "text/event-stream")
			if config.User != "" {
				req.SetBasicAuth(config.User, config.Pass)
			}
			// Using new context package from Go 1.7
			ctx, cancel := context.WithCancel(context.TODO())
			// initial request cancellation timer of 15s
			timer := time.AfterFunc(15*time.Second, func() {
				cancel()
				logger.Warn("No data for 15s, event stream request was cancelled")
			})
			req = req.WithContext(ctx)
			resp, err := client.Do(req)
			if err != nil {
				logger.WithFields(logrus.Fields{
					"error":    err.Error(),
					"endpoint": endpoint,
				}).Error("unable to access Marathon event stream")
				// expire request cancellation timer immediately
				timer.Reset(100 * time.Millisecond)
				continue
			}
			reader := bufio.NewReader(resp.Body)
			for {
				// reset request cancellation timer to 15s (should be >10s to avoid unnecessary reconnects
				// since ~10s seems to be the rate for dummy/keepalive events on the marathon event stream
				timer.Reset(15 * time.Second)
				line, err := reader.ReadString('\n')
				if err != nil {
					logger.WithFields(logrus.Fields{
						"error":    err.Error(),
						"endpoint": endpoint,
					}).Error("error reading Marathon event stream")
					resp.Body.Close()
					break
				}
				if !strings.HasPrefix(line, "event: ") {
					continue
				}
				logger.WithFields(logrus.Fields{
					"event":    strings.TrimSpace(line[6:]),
					"endpoint": endpoint,
				}).Info("marathon event received")
				select {
				case eventqueue <- true: // add reload to our queue channel, unless it is full of course.
				default:
					logger.Warn("queue is full")
				}
			}
			resp.Body.Close()
			logger.Warn("event stream connection was closed, re-opening")
		}
	}()
}

func endpointHealth() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		for {
			select {
			case <-ticker.C:
				for i, es := range health.Endpoints {
					client := &http.Client{
						Timeout:   5 * time.Second,
						Transport: tr,
					}
					req, err := http.NewRequest("GET", es.Endpoint+"/ping", nil)
					if err != nil {
						logger.WithFields(logrus.Fields{
							"error":    err.Error(),
							"endpoint": es.Endpoint,
						}).Error("an error occurred creating endpoint health request")
						health.Endpoints[i].Healthy = false
						health.Endpoints[i].Message = err.Error()
						continue
					}
					if config.User != "" {
						req.SetBasicAuth(config.User, config.Pass)
					}
					resp, err := client.Do(req)
					if err != nil {
						logger.WithFields(logrus.Fields{
							"error":    err.Error(),
							"endpoint": es.Endpoint,
						}).Error("endpoint is down")
						health.Endpoints[i].Healthy = false
						health.Endpoints[i].Message = err.Error()
						continue
					}
					resp.Body.Close()
					if resp.StatusCode != 200 {
						logger.WithFields(logrus.Fields{
							"status":   resp.StatusCode,
							"endpoint": es.Endpoint,
						}).Error("endpoint check failed")
						health.Endpoints[i].Healthy = false
						health.Endpoints[i].Message = resp.Status
						continue
					}
					health.Endpoints[i].Healthy = true
					health.Endpoints[i].Message = "OK"
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
				reload()
			}
		}
	}()
}

func fetchApps(jsonapps *MarathonApps) error {
	var endpoint string
	for _, es := range health.Endpoints {
		if es.Healthy == true {
			endpoint = es.Endpoint
			break
		}
	}
	if endpoint == "" {
		err := errors.New("all endpoints are down")
		return err
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
	}
	// fetch all apps and tasks with a single request.
	req, err := http.NewRequest("GET", endpoint+"/v2/apps?embed=apps.tasks", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if config.User != "" {
		req.SetBasicAuth(config.User, config.Pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&jsonapps)
	if err != nil {
		return err
	}
	return nil
}

func syncApps(jsonapps *MarathonApps) bool {
	config.Lock()
	defer config.Unlock()
	apps := make(map[string]App)
	for _, app := range jsonapps.Apps {
		var newapp = App{}
		for _, task := range app.Tasks {
			// lets skip tasks that does not expose any ports.
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
			var newtask = Task{}
			newtask.Host = task.Host
			newtask.Ports = task.Ports
			newtask.ServicePorts = task.ServicePorts
			newtask.StagedAt = task.StagedAt
			newtask.StartedAt = task.StartedAt
			newtask.Version = task.Version
			newapp.Tasks = append(newapp.Tasks, newtask)
		}
		// Lets ignore apps if no tasks are available
		if len(newapp.Tasks) > 0 {
			if s, ok := app.Labels["subdomain"]; ok {
				hosts := strings.Split(s, " ")
				for _, host := range hosts {
					newapp.Hosts = append(newapp.Hosts, host)
				}
			} else if s, ok := app.Labels["moxy_subdomain"]; ok {
				// to be compatible with moxy
				hosts := strings.Split(s, " ")
				for _, host := range hosts {
					newapp.Hosts = append(newapp.Hosts, host)
				}
			} else {
				// If directories are used lets use them as subdomain dividers.
				// Ex: /project/app becomes app.project
				// Ex: /app becomes just app
				domains := strings.Split(app.Id[1:], "/")
				for i, j := 0, len(domains)-1; i < j; i, j = i+1, j-1 {
					domains[i], domains[j] = domains[j], domains[i]
				}
				host := strings.Join(domains, ".")
				newapp.Hosts = append(newapp.Hosts, host)
			}
			// Check for duplicated subdomain labels
			for _, confapp := range apps {
				for _, host := range confapp.Hosts {
					for _, newhost := range newapp.Hosts {
						if newhost == host {
							logger.WithFields(logrus.Fields{
								"app":       app.Id,
								"subdomain": host,
							}).Warn("duplicate subdomain label")
							// reset hosts if duplicate.
							newapp.Hosts = nil
						}
					}
				}
			}
			// Got duplicated subdomains, lets ignore this one.
			if len(newapp.Hosts) == 0 {
				continue
			}
			newapp.Labels = app.Labels
			newapp.Env = app.Env
			for _, pds := range app.PortDefinitions {
				pd := PortDefinitions{
					Port:     pds.Port,
					Protocol: pds.Protocol,
					Labels:   pds.Labels,
				}
				newapp.PortDefinitions = append(newapp.PortDefinitions, pd)
			}
			apps[app.Id] = newapp
		}
	}
	// Not all events bring changes, so lets see if anything is new.
	eq := reflect.DeepEqual(apps, config.Apps)
	if eq {
		return true
	} else {
		config.Apps = apps
		return false
	}
}

func writeConf() error {
	config.RLock()
	defer config.RUnlock()

	template, err := template.New(filepath.Base(config.Nginx_template)).
		Delims(config.Left_delimiter, config.Right_delimiter).
		ParseFiles(config.Nginx_template)

	fmt.Println(template)

	if err != nil {
		return err
	}

	parent := filepath.Dir(config.Nginx_config)
	tmpFile, err := ioutil.TempFile(parent, ".nginx.conf.tmp-")
	defer tmpFile.Close()

	err = template.Execute(tmpFile, config)
	if err != nil {
		return err
	}
	config.LastUpdates.LastConfigRendered = time.Now()
	err = checkConf(tmpFile.Name())
	if err != nil {
		return err
	}
	err = os.Rename(tmpFile.Name(), config.Nginx_config)
	if err != nil {
		return err
	}
	return nil
}

func checkTmpl() error {
	config.RLock()
	defer config.RUnlock()
	t, err := template.New(filepath.Base(config.Nginx_template)).
		Delims(config.Left_delimiter, config.Right_delimiter).
		ParseFiles(config.Nginx_template)

	if err != nil {
		return err
	}
	err = t.Execute(ioutil.Discard, config)
	if err != nil {
		return err
	}
	return nil
}

func checkConf(path string) error {
	// This is to allow arguments as well. Example "docker exec nginx..."
	args := strings.Fields(config.Nginx_cmd)
	head := args[0]
	args = args[1:len(args)]
	args = append(args, "-c")
	args = append(args, path)
	args = append(args, "-t")
	cmd := exec.Command(head, args...)
	//cmd := exec.Command(parts..., "-c", path, "-t")
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
	// This is to allow arguments as well. Example "docker exec nginx..."
	args := strings.Fields(config.Nginx_cmd)
	head := args[0]
	args = args[1:len(args)]
	args = append(args, "-s")
	args = append(args, "reload")
	cmd := exec.Command(head, args...)
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

func reload() {
	start := time.Now()
	jsonapps := MarathonApps{}
	err := fetchApps(&jsonapps)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("unable to sync from marathon")
		go statsCount("reload.failed", 1)
		return
	}
	equal := syncApps(&jsonapps)
	if equal {
		logger.Info("no config changes")
		return
	}
	config.LastUpdates.LastSync = time.Now()
	err = writeConf()
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("unable to generate nginx config")
		go statsCount("reload.failed", 1)
		return
	}
	config.LastUpdates.LastConfigValid = time.Now()
	err = reloadNginx()
	if err != nil {
		logger.WithFields(logrus.Fields{
			"error": err.Error(),
		}).Error("unable to reload nginx")
		go statsCount("reload.failed", 1)
		return
	}
	elapsed := time.Since(start)
	logger.WithFields(logrus.Fields{
		"took": elapsed,
	}).Info("config updated")
	go statsCount("reload.success", 1)
	go statsTiming("reload.time", elapsed)
	config.LastUpdates.LastNginxReload = time.Now()
	return
}
