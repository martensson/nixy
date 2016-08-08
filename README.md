# nixy [![Build Status](https://travis-ci.org/martensson/nixy.svg?branch=master)](https://travis-ci.org/martensson/nixy)
![nginx
gopher](https://raw.githubusercontent.com/martensson/nixy/master/nixy-gopher.png)

Nixy is a daemon that automatically configures Nginx for web services deployed on [Apache Mesos](http://mesos.apache.org) and [Marathon](https://mesosphere.github.io/marathon/).

**Features:**

* Reverse proxy and load balancer for your microservices running inside Mesos and Marathon
* Single binary with no other dependencies *(except Nginx/Openresty)*
* Written in Go to be blazingly fast and concurrent.
* All the features you would expect from Nginx:
    * HTTP/TCP/UDP load balancing, HTTP/2 termination, websockets, SSL/TLS termination, caching/compression, authentication, media streaming, static file serving, etc.
* Zero downtime with Nginx fall-back mechanism for sick backends and hot config reload.
* Easy to customize your needs with templating.
* Statistics via statsd *(successful/failed updates, timings)*.
* Real-time updates via Marathon's event stream *(Marathon v0.9.0), so no need for callbacks.*
* Support for Marathon HA cluster, auto detects sick endpoints.
* Automatic service discovery of all running tasks inside Mesos/Marathon, including their health status.
* Basic auth support.
* Health checks for errors in template, nginx config or Marathon endpoints.
* ....

## Compatibility

- All versions of Marathon >= v0.9.0
- All versions of Nginx. Also compatible with [OpenResty](http://openresty.org/en/).

## Getting started

1. Install nixy from pre-compiled packages. Check `releases` page.
2. Edit config *(default on ubuntu is /etc/nixy.toml)*:

    ``` toml
    # nixy listening port
    port = "6000"
    # optional X-Proxy header name
    xproxy = "hostname"
    # marathon api
    marathon = ["http://example01:8080", "http://example02:8080"] # add all HA cluster nodes in priority order.
    user = "" # leave empty if no auth is required.
    pass = ""
    # nginx
    nginx_config = "/etc/nginx/nginx.conf"
    nginx_template = "/etc/nginx/nginx.tmpl"
    nginx_cmd = "nginx" # optionally openresty
    # statsd settings
    [statsd]
    addr = "localhost:8125" # optional for statistics
    #namespace = "nixy.my_mesos_cluster"
    #sample_rate = 100
    ```

3. Optionally edit the nginx template *(default on ubuntu is /etc/nginx/nginx.tmpl)*
4. Install [nginx](http://nginx.org/en/download.html) or [openresty](https://openresty.org/) and start the service.
5. Start nixy! *(service nixy start)*

## Using Nixy

Routing is based on the HTTP Host header matching app name by default.

This is easy to change and customize to your own choosing by editing the
nginx.tmpl file. For example if you prefer routing based on uri instead of subdomains.

Example to access your apps app1,app2,app3 running inside Mesos and Marathon:

    curl -i localhost/ -H 'Host: app1.example.com'
    curl -i localhost/ -H 'Host: app2.example.com'
    curl -i localhost/ -H 'Host: app3.example.com'

Assuming you have configured nginx on port 80.

### To set a custom subdomain for an application

Deploy your app to Marathon setting a custom label called `subdomain`:

    "labels": {
        "subdomain": "foobar"
    },

This will override the `Host` for that app and replace it with `foobar` as the new subdomain/host.

It's also possible to add multiple subdomains to a single app, dividing by a space character.

    "labels": {
        "subdomain": "foo bar"
    },

This will now match both `foo` and `bar` as the new subdomain/host.

### Template

Nixy uses the standard Go (Golang) [template package](https://golang.org/pkg/text/template/) to generate its config. It's a powerful and easy to use language to fully customize the nginx config. The default template is meant to be a working base that adds some sane defaults for Nginx. If needed just extend it or modify to suite your environment the best.

If you are unsure of what variables you can use inside your template just do a `GET /v1/config` and you will receive a JSON response of everything available. All labels and environment variables are available. Other options could be to enable websockets, HTTP/2, SSL/TLS, or to control ports, logging, load balancing method, or any other custom settings your applications need.

#### HTTP Load Balancing / Proxy

Examples:

**Add some ACL rules to block traffic from outside the internal network? Add a Label called `internal` to your app and the following snippet to your template:**
```
{{- if $app.Labels.internal}}
# allow anyone from local network.
allow 10.0.0.0/8;
# block everyone else
deny all;
{{- end }}
```

**Optionally, add dynamically which network that have access to the same label:**
```
{{- if $app.Labels.internal}}
# allow anyone from local network.
allow {{ $app.Labels.internal }};
# block everyone else
deny all;
{{- end }}
```

**Add a custom http header based on an Environment variable inside your app?**
```
{{- if $app.Env.APP_ENV}}
# could be dev, stage, production...
add_header X-Environment {{ $app.Env.APP_ENV }} always;
{{- end}}
```

#### TCP/UDP Load Balancing / Proxy

It is possible to use Nixy to configure nginx to be a proxy for TCP or UDP traffic.

Please check the `nginx-stream.tmpl` example and adapt it your own needs. It assumes you have configured `PortDefinitions` correctly for all your services.

You will need the latest NGINX Open Source built with the --with-stream configuration flag, or latest NGINX Plus.

### Nixy API

- `GET /` prints nixy version.
- `GET /v1/config` JSON response with all variables available inside the template.
- `GET /v1/reload` manually trigger a new config reload.
- `GET /v1/health` JSON response with health status of template, nginx config and Marathon endpoints available.

### Nagios Monitoring

In case you want to monitor nixy using Nagios (or compatible monitoring) you can use the included `check_nixy` plugin.
