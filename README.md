# nixy [![Build Status](https://travis-ci.org/martensson/nixy.svg?branch=master)](https://travis-ci.org/martensson/nixy)
![nginx
gopher](https://raw.githubusercontent.com/martensson/nixy/master/nixy-gopher.png)

Nixy is a daemon that automatically configures Nginx for web services deployed on [Apache Mesos](http://mesos.apache.org) and [Marathon](https://mesosphere.github.io/marathon/). It's an evolution of [moxy](https://github.com/martensson/moxy) but with a greatly improved feature set thanks to the Nginx reverse proxy.

**Features:**

* Reverse proxy and load balancer for your microservices running inside Mesos and Marathon
* Single binary with no other dependencies *(except Nginx/Openresty)*
* Written in Go to be blazingly fast and concurrent.
* All the features you get with Nginx:
    * HTTP/TCP load balancing, HTTP/2 termination, websockets, SSL/TLS termination, caching/compression, authentication, media streaming, static file serving, etc.
* Zero downtime with Nginx fallback mechanism for sick backends and hot config reload.
* Easy to customize with templating.
* Statistics via statsd *(successfull/failed updates, timings)*.
* Real-time updates via Marathon's event stream *(Marathon v0.9.0), no need for callbacks.*
* Automatic service discovery of all running tasks inside Mesos/Marathon, including health status.
* Basic auth support.
* Health check probe for errors in template or nginx configuration.
* + more...

## Compatibility

All versions of Marathon >= v0.9.0

## Getting started

1. Install nixy from pre-compiled packages. Check `releases` page.
2. Edit config (default on ubuntu is /etc/nixy.toml):
    ``` toml
    # nixy listening port
    port = "6000"

    # optional X-Proxy header name
    xproxy = "hostname"
    
    # marathon api
    marathon = "http://localhost:8080"
    user = "" # leave empty if no auth is required.
    pass = ""
    
    # nginx
    nginx_config = "/etc/nginx/nginx.conf"
    nginx_template = "/etc/nginx/nginx.tmpl"
    nginx_cmd = "nginx" # optinally openresty
    
    # statsd settings
    statsd = "localhost:8125" # optional for statistics
    ``` 
3. Install [nginx](http://nginx.org/en/download.html) or [openresty](https://openresty.org/) and start the service.
4. start the nixy service!

## Using Nixy

Routing is based on the HTTP Host header matching app name by default.

This is easy to change and customize to your own choosing by editing the
nginx.tmpl file. For example if you prefer routing based on uri instead of subdomains.

Example to access your apps app1,app2,app3 running inside Mesos and Marathon:

    curl -i localhost/ -H 'Host: app1.example.com'
    curl -i localhost/ -H 'Host: app2.example.com'
    curl -i localhost/ -H 'Host: app3.example.com'

Assuming you have configured nginx on port 80.

### To set custom subdomain for an application

Deploy your app to Marathon setting a custom label called `subdomain`:

    "labels": {
        "subdomain": "foobar"
    },

This will override the application name and replace it with `foobar` as the new subdomain/host-header.

### nixy api

- `GET /` prints nixy version
- `GET /v1/stats` for traffic statistics
- `GET /v1/config` list all variables available inside the template
- `GET /v1/reload` trigger a config regen
- `GET /v1/health` Responds 200 OK if template AND config is ok, else 500 Server Error with error message.
