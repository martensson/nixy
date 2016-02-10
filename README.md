# nixy [![Build Status](https://travis-ci.org/martensson/nixy.svg?branch=master)](https://travis-ci.org/martensson/nixy)

Nixy is a daemon that automatically configures Nginx for web services deployed on [Apache Mesos](http://mesos.apache.org) and [Marathon](https://mesosphere.github.io/marathon/). It's an evolution of [moxy](https://github.com/martensson/moxy) but with a greatly improved feature set thanks to the Nginx reverse proxy.

Features:

* Reverse proxy and load balancer for your microservices running inside Mesos and Marathon
* All features of Nginx, loadbalancing, websockets, HTTPS, HTTP/2, caching, static file serving, etc.
* Easy to customize with templating.
* Single binary with no other dependencies (except Nginx/Openresty)
* Statistics via statsd (successfull/failed updates, timings)
* Automatic service discovery of all running tasks inside Mesos/Marathon
* Uses the newer event stream (added in Marathon v0.9.0), no need to use callbacks.
* Health check url for your template and nginx configuration
* + more on the works...

## Compatibility

All versions of Marathon >= v0.9.0

## Getting started

1. Install nixy from pre-compiled packages. Check `releases` page.
2. Edit config (default on ubuntu is /etc/nixy.toml):
    ``` toml
    # nixy listening port
    port = "6000"

    # optional X-Proxy header added in all http responses
    xproxy = "hostname"

    # marathon api
    marathon = "http://localhost:8080"

    # nginx
    nginx_config = "/etc/nginx/nginx.conf"
    nginx_template = "/etc/nginx/nginx.tmpl"
    nginx_cmd = "nginx" # could be openresty

    # statsd settings
    statsd = "localhost:8125" # optional if you want statistics
    ``` 
3. Install nginx or openresty and start service.
4. Run nixy!

## Using Nixy

Routing is based on the HTTP Host header matching app name by default.

This is easy to change and customize to your own choosing by editing the
nginx.tmpl file.

Example to access your apps app1,app2,app3 running inside Mesos and Marathon:

    curl -i localhost/ -H 'Host: app1.example.com'
    curl -i localhost/ -H 'Host: app2.example.com'
    curl -i localhost/ -H 'Host: app3.example.com'

Assuming you have nginx listening in port 80.

### To set custom subdomain for an application

Deploy your app to Marathon setting a custom label called `subdomain`:

    "labels": {
        "subdomain": "foobar"
    },

This will override the application name and replace it with `foobar` as the new subdomain/host-header.

### nixy api

- `GET /` prints nixy version
- `GET /v1/stats` for traffic statistics
- `GET /v1/apps` list apps and running tasks used to generate nginx config
- `GET /v1/reload` trigger a config regen
- `GET /v1/health` Responds 200 OK if template AND config is ok, else 500 Server Error with error message.
