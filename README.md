# nixy [![Build Status](https://travis-ci.org/martensson/nixy.svg?branch=master)](https://travis-ci.org/martensson/nixy)

Nixy is a daemon that automatically configures Nginx for web services deployed on [Apache Mesos](http://mesos.apache.org) and [Marathon](https://mesosphere.github.io/marathon/). It's an evolution of [moxy](https://github.com/martensson/moxy) but with a greatly improved feature set thanks to the Nginx reverse proxy.

Features:

* Reverse proxy and load balancer for your microservices running inside Mesos and Marathon
* All features of Nginx, loadbalancing, caching, etc.
* Easy to customize with simple templating.
* Single binary with no other dependencies except Nginx/Openresty
* Statistics via statsd
* Automatic service discovery of all running tasks inside Mesos/Marathon
* Health check for your template and nginx configuration
* + more on the works...

## Compatibility

All versions of Marathon > 0.9

## Getting started

1. Easiest is to install nixy from pre-compiled packages. Check `releases` page.

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

Routing is based on the HTTP Host header matching app.* by default.

This is easy to change and customize to your own choosing by editing the
nginx.tmpl file.

Example to access your apps app1,app2,app3 running in Mesos and Marathon:

    curl -i localhost:7000/ -H 'Host: app1.example.com'
    curl -i localhost:7000/ -H 'Host: app2.example.com'
    curl -i localhost:7000/ -H 'Host: app3.example.com'

### To set custom subdomain for an application

Deploy your app to Marathon setting a custom label called `subdomain`:

    "labels": {
        "subdomain": "foobar"
    },

This will override the application name and replace it with `foobar` as the new subdomain/host-header.

### Check state of Nixy

- `/v1/stats` for traffic statistics

- `/v1/apps` list apps and running tasks for load balancing

- `/v1/reload` trigger a config regen

- `/v1/health` Responds 200 OK if template and config is ok, else responds 500 Server Error.
